package main

import (
    "bufio"
    "flag"
    "fmt"
    "os"
    "path/filepath"
    "sort"
    "strings"
    "sync/atomic"
    "syscall"
    "text/tabwriter"
    "time"
)

// Program version
var version = "dev"

// FolderSize holds info about a folder:
// Path    - the folder path
// Size    - total file size in bytes
// Skipped - true if the folder was skipped (time threshold)
type FolderSize struct {
    Path    string
    Size    int64
    Skipped bool
}

// NetworkMount holds info about a network mount:
// MountPoint  - the mount path
// FsType      - e.g. "nfs", "cifs", "dav"
// SizeBytes   - total capacity
// UsedBytes   - used space
// FreeBytes   - free space
// DisplayName - e.g. "Network FS (nfs)"
type NetworkMount struct {
    MountPoint  string
    FsType      string
    SizeBytes   uint64
    UsedBytes   uint64
    FreeBytes   uint64
    DisplayName string
}

// formatSize converts bytes to a human-readable string (GB, MB, KB).
func formatSize(size int64) string {
    switch {
    case size >= 1<<30:
        return fmt.Sprintf("%.2f GB", float64(size)/(1<<30))
    case size >= 1<<20:
        return fmt.Sprintf("%.2f MB", float64(size)/(1<<20))
    default:
        return fmt.Sprintf("%d KB", size/(1<<10))
    }
}

// formatSizeUint64 is the same but for uint64.
func formatSizeUint64(size uint64) string {
    switch {
    case size >= 1<<30:
        return fmt.Sprintf("%.2f GB", float64(size)/(1<<30))
    case size >= 1<<20:
        return fmt.Sprintf("%.2f MB", float64(size)/(1<<20))
    default:
        return fmt.Sprintf("%d KB", size/(1<<10))
    }
}

// isExcluded checks whether a directory should be skipped (e.g., /proc, /sys, /dev).
func isExcluded(path string) bool {
    base := filepath.Base(path)
    switch base {
    case "proc", "sys", "dev", "run", "tmp", "var":
        return true
    default:
        return false
    }
}

// getDiskUsageInfo returns total, free, and used bytes for the filesystem containing 'path'.
func getDiskUsageInfo(path string) (total, free, used uint64, err error) {
    var stat syscall.Statfs_t
    if err := syscall.Statfs(path, &stat); err != nil {
        return 0, 0, 0, err
    }
    total = stat.Blocks * uint64(stat.Bsize)
    free = stat.Bfree * uint64(stat.Bsize)
    used = total - free
    return total, free, used, nil
}

// detectNetworkFileSystems reads /proc/mounts (Linux) to find network-like mounts
// (nfs, cifs, dav, fuse, etc.). Returns a map of mountpoint->fstype plus a list of NetworkMount.
func detectNetworkFileSystems() (map[string]string, []NetworkMount, error) {
    file, err := os.Open("/proc/mounts")
    if err != nil {
        return nil, nil, err
    }
    defer file.Close()

    netMap := map[string]string{}
    var nets []NetworkMount

    scanner := bufio.NewScanner(file)
    for scanner.Scan() {
        line := scanner.Text()
        parts := strings.Fields(line)
        if len(parts) < 3 {
            continue
        }
        mountPoint := parts[1]
        fsType := parts[2]

        // Check if it's a known "network-like" FS
        // e.g., nfs, cifs, smb, sshfs, ftp, http, dav, fuse...
        if strings.Contains(fsType, "nfs") ||
            strings.Contains(fsType, "cifs") ||
            strings.Contains(fsType, "smb") ||
            strings.Contains(fsType, "sshfs") ||
            strings.Contains(fsType, "ftp") ||
            strings.Contains(fsType, "http") ||
            strings.Contains(fsType, "dav") ||
            strings.Contains(fsType, "fuse") {
            // Attempt to get its size info
            t, f, u, errStat := getDiskUsageInfo(mountPoint)
            if errStat == nil {
                // Skip displaying if total=0 (empty FS)
                if t == 0 {
                    // We still add it to netMap so we do NOT walk it,
                    // but won't add it to the final 'nets' slice for display
                    netMap[mountPoint] = fsType
                    continue
                }
                netMap[mountPoint] = fsType
                nets = append(nets, NetworkMount{
                    MountPoint:  mountPoint,
                    FsType:      fsType,
                    SizeBytes:   t,
                    UsedBytes:   u,
                    FreeBytes:   f,
                    DisplayName: fmt.Sprintf("Network FS (%s)", fsType),
                })
            } else {
                // If Statfs fails, mark it anyway so we skip walking it,
                // but do not display
                netMap[mountPoint] = fsType
            }
        }
    }

    return netMap, nets, nil
}

// walkDirectory recursively sums file sizes under 'path'.
// If scanning takes longer than slowThreshold, returns Skipped=true.
func walkDirectory(path string, slowThreshold time.Duration, start time.Time, networkFS map[string]string) (int64, bool) {
    var totalSize int64

    err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
        if err != nil {
            return filepath.SkipDir
        }
        // If p is a network mount, skip
        if _, ok := networkFS[p]; ok {
            return filepath.SkipDir
        }
        // If p is excluded (system dirs)
        if isExcluded(p) {
            return filepath.SkipDir
        }
        // Check time
        if time.Since(start) > slowThreshold {
            return fmt.Errorf("slow directory")
        }
        // Sum file size
        if !info.IsDir() {
            totalSize += info.Size()
        }
        return nil
    })
    if err != nil {
        return totalSize, true
    }
    return totalSize, false
}

// listAllDirs collects all directories under 'root' (recursively),
// excluding any known network mount points.
func listAllDirs(root string, networkFS map[string]string) []string {
    var dirs []string
    filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
        if err != nil {
            return nil
        }
        if info.IsDir() {
            // If p is a network mount, skip
            if _, ok := networkFS[p]; !ok {
                dirs = append(dirs, p)
            }
        }
        return nil
    })
    return dirs
}

// findAllSubfolders walks all directories to compute their sizes.
// If a directory is too slow, we mark it Skipped.
func findAllSubfolders(root string, progress *int32, totalItems int32, slowThreshold time.Duration, networkFS map[string]string) []FolderSize {
    var subfolders []FolderSize
    dirs := listAllDirs(root, networkFS)

    for _, dir := range dirs {
        start := time.Now()
        size, skipped := walkDirectory(dir, slowThreshold, start, networkFS)
        subfolders = append(subfolders, FolderSize{
            Path:    dir,
            Size:    size,
            Skipped: skipped,
        })
        atomic.AddInt32(progress, 1)
    }

    // Sort descending by size
    sort.Slice(subfolders, func(i, j int) bool {
        return subfolders[i].Size > subfolders[j].Size
    })
    return subfolders
}

func main() {
    // Command-line flags
    flag.Usage = func() {
        fmt.Println("Usage: find-large-dirs [directory] [--top <N>] [--slow-threshold <duration>] [--help] [--version]")
        fmt.Println("Recursively scan the given directory and show the largest subdirectories.")
        fmt.Println("If no directory is provided, defaults to '/'.")
        fmt.Println("Example: find-large-dirs /home/user --top 10 --slow-threshold=3s")
    }
    help := flag.Bool("help", false, "Show help")
    top := flag.Int("top", 20, "Number of top largest folders to display")
    slowThreshold := flag.Duration("slow-threshold", 2*time.Second, "Max duration before skipping a directory")
    showVersion := flag.Bool("version", false, "Show program version")
    flag.Parse()

    if *help {
        flag.Usage()
        return
    }
    if *showVersion {
        fmt.Printf("find-large-dirs version: %s\n", version)
        return
    }

    // If no argument, default to "/"
    path := "/"
    if flag.NArg() > 0 {
        path = flag.Arg(0)
    }

    // Detect network mounts first
    networkFSMap, networkMounts, err := detectNetworkFileSystems()
    if err != nil {
        fmt.Printf("Warning: could not detect network filesystems: %v\n", err)
    }

    // Decide whether to show disk stats or folder stats
    showDiskStats := false
    if path == "/" {
        showDiskStats = true
    } else {
        // For simplicity, only root "/" shows disk usage.
        showDiskStats = false
    }

    if showDiskStats {
        total, free, used, err := getDiskUsageInfo(path)
        if err == nil {
            fmt.Printf("Mount point: %s\n", path)
            fmt.Printf("Total: %s   Used: %s   Free: %s\n\n",
                formatSizeUint64(total),
                formatSizeUint64(used),
                formatSizeUint64(free),
            )
        } else {
            fmt.Printf("Could not retrieve disk info for %s\n\n", path)
        }
    } else {
        // If a specific folder was provided, compute its total size
        start := time.Now()
        folderSize, skipped := walkDirectory(path, *slowThreshold, start, networkFSMap)
        if skipped {
            fmt.Printf("Folder '%s': size calculation skipped (too slow)\n\n", path)
        } else {
            fmt.Printf("Folder '%s': %s\n\n", path, formatSize(folderSize))
        }
    }

    // Build the directory list for progress
    dirs := listAllDirs(path, networkFSMap)
    totalItems := int32(len(dirs))
    var progress int32

    // Show progress in a separate goroutine
    go func() {
        for {
            current := atomic.LoadInt32(&progress)
            pct := 0.0
            if totalItems > 0 {
                pct = float64(current) / float64(totalItems) * 100
            }
            fmt.Printf("\rProgress: %.2f%%", pct)
            time.Sleep(200 * time.Millisecond)
            if current >= totalItems {
                break
            }
        }
    }()

    // Recursively compute all folders
    subfolders := findAllSubfolders(path, &progress, totalItems, *slowThreshold, networkFSMap)

    // New line after progress
    fmt.Println()

    // Print network filesystems (excluding 0-sized ones, already skipped)
    if len(networkMounts) > 0 {
        fmt.Println("Network filesystems detected:")
        wNet := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
        // Header
        fmt.Fprintf(wNet, "TYPE\tMOUNT POINT\tTOTAL\tUSED\tFREE\n")
        for _, nm := range networkMounts {
            fmt.Fprintf(wNet, "%s\t%s\t%s\t%s\t%s\n",
                nm.DisplayName,
                nm.MountPoint,
                formatSizeUint64(nm.SizeBytes),
                formatSizeUint64(nm.UsedBytes),
                formatSizeUint64(nm.FreeBytes),
            )
        }
        wNet.Flush()
        fmt.Println()
    }

    // Print top N largest directories
    fmt.Printf("Top %d largest directories in '%s':\n", *top, path)
    w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)

    // We'll print columns: SIZE, PATH (and if skipped, append "(skipped)" to the path).
    for i, folder := range subfolders {
        if i >= *top {
            break
        }
        displayPath := folder.Path
        if folder.Skipped {
            // Append "(skipped)" right after the path
            displayPath += " (skipped)"
        }
        // Example line:  "8.41 GB    /home/matvey/isotope-pathways (skipped)"
        fmt.Fprintf(w, "%-10s\t%-60s\n",
            formatSize(folder.Size),
            displayPath,
        )
    }
    w.Flush()
}

