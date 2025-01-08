package main

import (
    "bufio"
    "container/list"
    "flag"
    "fmt"
    "io/ioutil"
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

// FolderSize holds info about a folder
type FolderSize struct {
    Path    string
    Size    int64
    Skipped bool
}

// NetworkMount holds info about a network filesystem
type NetworkMount struct {
    MountPoint  string
    FsType      string
    DisplayName string
}

// formatSize converts bytes to a human-readable string.
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

// formatSizeUint64 for uint64
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

// shortenPath truncates the path for display so it won't wrap too much.
func shortenPath(path string, maxLen int) string {
    if len(path) <= maxLen {
        return path
    }
    return path[:maxLen-3] + "..."
}

// isExcluded checks if a directory should be skipped (e.g. /proc, /sys).
func isExcluded(path string) bool {
    base := filepath.Base(path)
    switch base {
    case "proc", "sys", "dev", "run", "tmp", "var":
        return true
    default:
        return false
    }
}

// getDiskUsageInfo returns total, free, and used bytes for the filesystem.
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

// detectNetworkFileSystems reads /proc/mounts to find network-like FS
func detectNetworkFileSystems() (map[string]string, []NetworkMount, error) {
    f, err := os.Open("/proc/mounts")
    if err != nil {
        return nil, nil, err
    }
    defer f.Close()

    netMap := make(map[string]string)
    var nets []NetworkMount

    scanner := bufio.NewScanner(f)
    for scanner.Scan() {
        line := scanner.Text()
        fields := strings.Fields(line)
        if len(fields) < 3 {
            continue
        }
        mountPoint := fields[1]
        fsType := fields[2]

        // Check if it's a "network-like" FS
        if strings.Contains(fsType, "nfs") ||
            strings.Contains(fsType, "cifs") ||
            strings.Contains(fsType, "smb") ||
            strings.Contains(fsType, "sshfs") ||
            strings.Contains(fsType, "ftp") ||
            strings.Contains(fsType, "http") ||
            strings.Contains(fsType, "dav") {
            netMap[mountPoint] = fsType
            nets = append(nets, NetworkMount{
                MountPoint:  mountPoint,
                FsType:      fsType,
                DisplayName: fmt.Sprintf("Network FS (%s)", fsType),
            })
        }
    }
    return netMap, nets, nil
}

// bfsCountDirs counts how many directories we'll process, for progress.
func bfsCountDirs(root string, netMap map[string]string) int {
    count := 0
    queue := list.New()
    queue.PushBack(root)

    for queue.Len() > 0 {
        e := queue.Front()
        queue.Remove(e)
        dirPath := e.Value.(string)

        // Skip if network mount or excluded
        if _, ok := netMap[dirPath]; ok {
            continue
        }
        if isExcluded(dirPath) {
            continue
        }
        count++

        entries, err := ioutil.ReadDir(dirPath)
        if err != nil {
            continue
        }
        for _, fi := range entries {
            if fi.IsDir() {
                queue.PushBack(filepath.Join(dirPath, fi.Name()))
            }
        }
    }
    return count
}

// bfsComputeSizes does one BFS pass for minimal overhead.
func bfsComputeSizes(
    root string,
    netMap map[string]string,
    slowThreshold time.Duration,
    opChan chan<- string,
    progress *int32,
    totalDirs int32,
) []FolderSize {

    resultsMap := make(map[string]*FolderSize)
    queue := list.New()
    queue.PushBack(root)

    resultsMap[root] = &FolderSize{Path: root, Size: 0, Skipped: false}

    for queue.Len() > 0 {
        e := queue.Front()
        queue.Remove(e)
        dirPath := e.Value.(string)

        // Skip network mount or excluded
        if _, ok := netMap[dirPath]; ok {
            resultsMap[dirPath] = &FolderSize{Path: dirPath, Skipped: true}
            atomic.AddInt32(progress, 1)
            continue
        }
        if isExcluded(dirPath) {
            resultsMap[dirPath] = &FolderSize{Path: dirPath, Skipped: true}
            atomic.AddInt32(progress, 1)
            continue
        }

        // Send operation name
        select {
        case opChan <- "Scanning " + dirPath:
        default:
        }

        start := time.Now()
        var localSize int64
        skipDir := false

        entries, err := ioutil.ReadDir(dirPath)
        if err != nil {
            skipDir = true
        } else {
            for _, fi := range entries {
                if time.Since(start) > slowThreshold {
                    skipDir = true
                    break
                }
                if !fi.IsDir() {
                    localSize += fi.Size()
                }
            }
        }

        res, found := resultsMap[dirPath]
        if !found {
            res = &FolderSize{Path: dirPath}
            resultsMap[dirPath] = res
        }
        res.Size = localSize
        res.Skipped = skipDir

        if !skipDir && err == nil {
            // Enqueue subdirs
            for _, fi := range entries {
                if fi.IsDir() {
                    subPath := filepath.Join(dirPath, fi.Name())
                    if _, ok := resultsMap[subPath]; !ok {
                        resultsMap[subPath] = &FolderSize{Path: subPath}
                    }
                    queue.PushBack(subPath)
                }
            }
        }

        atomic.AddInt32(progress, 1)
    }

    resultSlice := make([]FolderSize, 0, len(resultsMap))
    for _, fs := range resultsMap {
        resultSlice = append(resultSlice, *fs)
    }

    sort.Slice(resultSlice, func(i, j int) bool {
        return resultSlice[i].Size > resultSlice[j].Size
    })
    return resultSlice
}

func main() {
    flag.Usage = func() {
        fmt.Println("Usage: find-large-dirs [directory] [--top <N>] [--slow-threshold <duration>] [--help] [--version]")
        fmt.Println("Single-pass BFS, skipping network mounts and system dirs. Shows one-line progress.")
    }
    helpFlag := flag.Bool("help", false, "Show help")
    topFlag := flag.Int("top", 30, "Number of top largest folders to display")
    slowFlag := flag.Duration("slow-threshold", 2*time.Second, "Max time before skipping a directory")
    versionFlag := flag.Bool("version", false, "Show version")
    flag.Parse()

    if *helpFlag {
        flag.Usage()
        return
    }
    if *versionFlag {
        fmt.Printf("find-large-dirs version: %s\n", version)
        return
    }

    rootPath := "/"
    if flag.NArg() > 0 {
        rootPath = flag.Arg(0)
    }

    // Detect network FS
    netMap, netMounts, err := detectNetworkFileSystems()
    if err != nil {
        fmt.Fprintf(os.Stderr, "Warning: could not detect network FS: %v\n", err)
    }

    // If root is "/", show disk info
    if rootPath == "/" {
        total, free, used, err := getDiskUsageInfo(rootPath)
        if err == nil {
            fmt.Printf("Mount point: %s\n", rootPath)
            fmt.Printf("Total: %s   Used: %s   Free: %s\n\n",
                formatSizeUint64(total),
                formatSizeUint64(used),
                formatSizeUint64(free),
            )
        } else {
            fmt.Printf("Could not get disk info for '%s': %v\n\n", rootPath, err)
        }
    } else {
        // Quick top-level check
        var quickSize int64
        skipQuick := false
        start := time.Now()

        entries, err := ioutil.ReadDir(rootPath)
        if err != nil {
            skipQuick = true
        } else {
            for _, fi := range entries {
                if time.Since(start) > *slowFlag {
                    skipQuick = true
                    break
                }
                if !fi.IsDir() {
                    quickSize += fi.Size()
                }
            }
        }
        if skipQuick {
            fmt.Printf("Folder '%s': size calculation skipped (timed out)\n\n", rootPath)
        } else {
            fmt.Printf("Folder '%s': ~%s (top-level only)\n\n", rootPath, formatSize(quickSize))
        }
    }

    // Count total dirs for progress
    totalCount := bfsCountDirs(rootPath, netMap)
    totalDirs := int32(totalCount)
    var progress int32

    // Channels for operation and done
    opChan := make(chan string, 1)
    doneChan := make(chan bool)

    // Progress goroutine
    go func() {
        ticker := time.NewTicker(200 * time.Millisecond)
        defer ticker.Stop()

        var currentOp string
        for {
            select {
            case msg := <-opChan:
                currentOp = msg
            case <-ticker.C:
                cur := atomic.LoadInt32(&progress)
                pct := 0.0
                if totalDirs > 0 {
                    pct = float64(cur) / float64(totalDirs) * 100
                }
                // Erase line, move cursor to start
                fmt.Printf("\r\033[K")

                // Truncate path so it doesn't cause line wrapping
                shortPath := shortenPath(currentOp, 60)
                fmt.Printf("Progress: %.2f%% | %s", pct, shortPath)

                if cur >= totalDirs {
                    // Erase line at the end
                    fmt.Printf("\r\033[K")
                    doneChan <- true
                    return
                }
            }
        }
    }()

    // BFS to compute sizes
    resultFolders := bfsComputeSizes(rootPath, netMap, *slowFlag, opChan, &progress, totalDirs)

    // Wait for progress goroutine
    <-doneChan
    fmt.Println()

    // Print network FS
    if len(netMounts) > 0 {
        fmt.Println("Network filesystems detected (skipped entirely):")
        for _, nm := range netMounts {
            fmt.Printf("  %s => %s\n", nm.DisplayName, nm.MountPoint)
        }
        fmt.Println()
    }

    // Print top N largest
    fmt.Printf("Top %d largest directories in '%s':\n", *topFlag, rootPath)
    w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
    count := 0
    for _, fs := range resultFolders {
        if count >= *topFlag {
            break
        }
        dp := fs.Path
        if fs.Skipped {
            dp += " (skipped)"
        }
        fmt.Fprintf(w, "%-10s\t%-60s\n",
            formatSize(fs.Size),
            dp,
        )
        count++
    }
    w.Flush()
}

