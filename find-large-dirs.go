package main

import (
    // Core I/O libraries
    "bufio"
    "container/list"
    "flag"
    "fmt"
    "io/ioutil"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "sort"
    "strings"
    "sync/atomic"
    "syscall"
    "text/tabwriter"
    "time"
)

/*
    GLOBAL CONSTANTS & VARIABLES
    We place these here so they're easy to locate and modify as needed.
*/

// Program version is set at build time, or left as "dev" if not specified.
var version = "dev"

/*
    DATA STRUCTURES
    These structs hold the logical data we need for scanning directories.
*/

// FolderSize holds information about a folder's path, size, and whether it was skipped.
type FolderSize struct {
    Path    string
    Size    int64
    Skipped bool
}

// NetworkMount holds information about a "network-like" filesystem mount.
type NetworkMount struct {
    MountPoint  string
    FsType      string
    DisplayName string
}

/*
    UTILITY FUNCTIONS
    We group similar helper functions in one place for clarity.
*/

// formatSize converts an int64 byte count into a human-readable string (MB/GB).
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

// formatSizeUint64 converts a uint64 byte count into a human-readable string (MB/GB).
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

// shortenPath truncates a path to avoid long line wrapping in progress output.
func shortenPath(path string, maxLen int) string {
    if len(path) <= maxLen {
        return path
    }
    return path[:maxLen-3] + "..."
}

// isExcluded checks if a directory should be skipped (e.g., /proc, /sys, etc.).
func isExcluded(path string) bool {
    base := filepath.Base(path)
    switch base {
    case "proc", "sys", "dev", "run", "tmp", "var":
        return true
    default:
        return false
    }
}

/*
    DISK USAGE
    The functions below provide basic disk usage information (total, free, used).
*/

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

/*
    NETWORK FS DETECTION
    We only detect network file systems if the user is scanning the root directory.
    For each OS, we attempt a best-effort detection method:
      - Linux:    parse /proc/mounts
      - macOS:    parse output of `df -h`
      - FreeBSD:  parse `mount -l`
      - Windows:  parse `net use`
    If detection fails, we return an empty set. This is intentionally minimal and
    won't cover all corner cases in real-world usage but demonstrates the concept.
*/

// detectNetworkFileSystems tries to detect network-like FS *only if root is scanned*.
func detectNetworkFileSystems(rootPath string) (map[string]string, []NetworkMount, error) {
    // If the user is NOT scanning "/", or "C:\\", or something that indicates
    // root in their OS, skip detection entirely:
    if !isRootPath(rootPath) {
        return make(map[string]string), nil, nil
    }

    switch runtime.GOOS {
    case "linux":
        return detectNetworkFSLinux()
    case "darwin":
        return detectNetworkFSDarwin()
    case "freebsd", "openbsd", "netbsd", "dragonfly":
        return detectNetworkFSBSD()
    case "windows":
        return detectNetworkFSWindows()
    default:
        // If OS is unknown or unsupported, return empty
        return make(map[string]string), nil, nil
    }
}

/*
    HELPER: isRootPath
    Checks if the user-supplied path is effectively the root path on the current OS.
    We do a simplified check here just for demonstration. Real logic might be more involved.
*/
func isRootPath(path string) bool {
    switch runtime.GOOS {
    case "windows":
        // On Windows, root might be "C:\", "D:\", etc. We'll do a naive check:
        // We consider root if length is 3, e.g. "C:\" or "D:\".
        if len(path) == 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
            return true
        }
        return false
    default:
        // On Unix-like systems, root is "/"
        return path == "/"
    }
}

// detectNetworkFSLinux uses /proc/mounts to find NFS, CIFS, SMB, SSHFS, etc.
func detectNetworkFSLinux() (map[string]string, []NetworkMount, error) {
    f, err := os.Open("/proc/mounts")
    if err != nil {
        // If we can't read /proc/mounts, return an error
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
        if isNetworkLike(fsType) {
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

// detectNetworkFSDarwin uses 'df -h' and looks for known network FS patterns (very naive).
func detectNetworkFSDarwin() (map[string]string, []NetworkMount, error) {
    cmd := exec.Command("df", "-h")
    out, err := cmd.Output()
    if err != nil {
        return nil, nil, err
    }

    netMap := make(map[string]string)
    var nets []NetworkMount

    lines := strings.Split(string(out), "\n")
    for _, line := range lines {
        fields := strings.Fields(line)
        if len(fields) < 6 {
            continue
        }
        mountPoint := fields[len(fields)-1] // Last field is the mount point
        fsSpec := fields[0]                // The first field is the device or remote spec
        // Very naive check for remote spec
        if strings.Contains(fsSpec, ":/") || strings.HasPrefix(fsSpec, "//") {
            netMap[mountPoint] = "network-like"
            nets = append(nets, NetworkMount{
                MountPoint:  mountPoint,
                FsType:      "network-like",
                DisplayName: fmt.Sprintf("Network FS (%s)", fsSpec),
            })
        }
    }
    return netMap, nets, nil
}

// detectNetworkFSBSD uses 'mount -l' to attempt detection similarly.
func detectNetworkFSBSD() (map[string]string, []NetworkMount, error) {
    cmd := exec.Command("mount", "-l")
    out, err := cmd.Output()
    if err != nil {
        return nil, nil, err
    }

    netMap := make(map[string]string)
    var nets []NetworkMount

    lines := strings.Split(string(out), "\n")
    for _, line := range lines {
        // Typical format: <fsSpec> on <mountPoint> (<fsType>, ...)
        // We'll do a naive parse:
        parts := strings.Split(line, " ")
        if len(parts) < 4 {
            continue
        }
        mountPoint := parts[2]
        // The mount type is usually in parentheses at the end
        leftParen := strings.Index(line, "(")
        rightParen := strings.Index(line, ")")
        if leftParen >= 0 && rightParen > leftParen {
            fsType := line[leftParen+1 : rightParen]
            if isNetworkLike(fsType) {
                netMap[mountPoint] = fsType
                nets = append(nets, NetworkMount{
                    MountPoint:  mountPoint,
                    FsType:      fsType,
                    DisplayName: fmt.Sprintf("Network FS (%s)", fsType),
                })
            }
        }
    }
    return netMap, nets, nil
}

// detectNetworkFSWindows uses 'net use' for naive detection.
func detectNetworkFSWindows() (map[string]string, []NetworkMount, error) {
    cmd := exec.Command("net", "use")
    out, err := cmd.Output()
    if err != nil {
        return nil, nil, err
    }

    netMap := make(map[string]string)
    var nets []NetworkMount

    // 'net use' output might look like:
    //  Status       Local     Remote                    Network
    //  ---------------------------------------------------------------------
    //  OK           X:        \\SomeServer\ShareName    Microsoft Windows Network
    // We'll parse lines that have a '\\server\share'
    lines := strings.Split(string(out), "\n")
    for _, line := range lines {
        if strings.Contains(line, `\\`) {
            fields := strings.Fields(line)
            // Typically the second or third field might be the share name
            for _, f := range fields {
                if strings.HasPrefix(f, `\\`) {
                    // Mark the share as network-like
                    mountPt := f // It's not exactly a mount point as on Linux, but for demonstration
                    netMap[mountPt] = "windows-network"
                    nets = append(nets, NetworkMount{
                        MountPoint:  mountPt,
                        FsType:      "windows-network",
                        DisplayName: fmt.Sprintf("Network FS (%s)", mountPt),
                    })
                }
            }
        }
    }
    return netMap, nets, nil
}

/*
    HELPER: isNetworkLike
    Checks if the given fsType is typically network-based.
*/
func isNetworkLike(fsType string) bool {
    fsType = strings.ToLower(fsType)
    if strings.Contains(fsType, "nfs") ||
        strings.Contains(fsType, "cifs") ||
        strings.Contains(fsType, "smb") ||
        strings.Contains(fsType, "sshfs") ||
        strings.Contains(fsType, "ftp") ||
        strings.Contains(fsType, "http") ||
        strings.Contains(fsType, "dav") {
        return true
    }
    return false
}

/*
    SINGLE-PASS BFS SCAN
    - We scan directories in a single goroutine so as not to overload disk I/O.
    - We skip known network mounts (if scanning root) and “excluded” system dirs.
    - We measure how long it takes to read each directory; if it exceeds 'slowThreshold',
      we mark it as “skipped” and do not enqueue its children.
    - For concurrency, we ONLY use a goroutine to show progress in real-time.
      This avoids any parallel disk access.
*/

// bfsComputeSizes performs a SINGLE-PASS BFS, skipping known network or excluded dirs.
func bfsComputeSizes(
    root string,
    netMap map[string]string,
    slowThreshold time.Duration,
    dirChan chan<- string,      // channel for reporting "currently scanning X"
    resultChan chan<- FolderSize, // channel for final results (each folder)
) {

    // We'll store BFS state in a queue (from container/list).
    queue := list.New()
    queue.PushBack(root)

    // BFS iteration
    for queue.Len() > 0 {
        e := queue.Front()
        queue.Remove(e)
        dirPath := e.Value.(string)

        // Immediately notify the progress goroutine that we're scanning dirPath
        // (non-blocking send: if the channel is full, we skip).
        select {
        case dirChan <- dirPath:
        default:
        }

        // Skip network or excluded
        if _, ok := netMap[dirPath]; ok {
            // If it's a known network FS mount, skip entirely
            resultChan <- FolderSize{Path: dirPath, Size: 0, Skipped: true}
            continue
        }
        if isExcluded(dirPath) {
            // If it's an excluded system dir, skip entirely
            resultChan <- FolderSize{Path: dirPath, Size: 0, Skipped: true}
            continue
        }

        // Begin timing how long this directory read takes
        start := time.Now()
        var localSize int64
        skipDir := false

        entries, err := ioutil.ReadDir(dirPath)
        if err != nil {
            skipDir = true
        } else {
            for _, fi := range entries {
                // If we're taking too long, skip this directory
                if time.Since(start) > slowThreshold {
                    skipDir = true
                    break
                }
                // If it's a file, accumulate size
                if !fi.IsDir() {
                    localSize += fi.Size()
                }
            }
        }

        // Record result for this directory
        resultChan <- FolderSize{Path: dirPath, Size: localSize, Skipped: skipDir}

        // If we didn't skip, enqueue subdirectories
        if !skipDir && err == nil {
            for _, fi := range entries {
                if fi.IsDir() {
                    subPath := filepath.Join(dirPath, fi.Name())
                    queue.PushBack(subPath)
                }
            }
        }
    }
}

/*
    MAIN FUNCTION
    We coordinate all logic here:
      1) Parse flags
      2) Detect network FS if scanning root (to skip them)
      3) (Optional) Show disk usage info if scanning root
      4) Start BFS scanning in a single goroutine
      5) Start a progress goroutine to display which directory is being scanned
      6) Collect BFS results and sort them
      7) Print top N largest directories
*/
func main() {
    // 1) Parse flags
    flag.Usage = func() {
        fmt.Println("Usage: find-large-dirs [directory] [--top <N>] [--slow-threshold <duration>]")
        fmt.Println("[--help] [--version]")
        fmt.Println("Single-pass BFS, skipping network mounts and system dirs. Shows live progress.")
    }
    helpFlag := flag.Bool("help", false, "Show help")
    topFlag := flag.Int("top", 30, "Number of top largest folders to display")
    slowFlag := flag.Duration("slow-threshold", 2*time.Second, "Max time before skipping a directory")
    versionFlag := flag.Bool("version", false, "Show version")
    flag.Parse()

    // If help or version is requested, print and exit early.
    if *helpFlag {
        flag.Usage()
        return
    }
    if *versionFlag {
        fmt.Printf("find-large-dirs version: %s\n", version)
        return
    }

    // Determine the root path to scan. If no argument is given, default to "/".
    rootPath := "/"
    if flag.NArg() > 0 {
        rootPath = flag.Arg(0)
    }

    // 2) Detect network FS only if scanning the system root
    netMap, netMounts, err := detectNetworkFileSystems(rootPath)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Warning: could not detect network FS: %v\n", err)
    }

    // 3) If scanning root, try to display disk usage info
    if isRootPath(rootPath) {
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
    }

    /*
        4) & 5) Start BFS scanning & progress goroutine
        - We use one goroutine for BFS to avoid parallel disk I/O.
        - We use one goroutine for progress updates, to show the user which directory
          is currently being scanned and how many we've scanned so far.
    */
    dirChan := make(chan string, 1)       // receives "currently scanning" paths
    resultChan := make(chan FolderSize, 8) // receives the BFS results
    doneChan := make(chan bool)

    var scannedCount int64

    // Progress goroutine: continuously read from dirChan, increment scannedCount, and show progress.
    go func() {
        for {
            dirPath, ok := <-dirChan
            if !ok {
                // Channel closed => BFS is done
                break
            }
            newCount := atomic.AddInt64(&scannedCount, 1)

            // Print a short line "Scanned X dirs so far, currently scanning Y"
            // Erase line, move cursor to start:
            fmt.Printf("\r\033[K")
            shortPath := shortenPath(dirPath, 60)
            fmt.Printf("Scanned %d dirs so far... current: %s", newCount, shortPath)
        }
        // When BFS completes, signal main
        doneChan <- true
    }()

    // BFS in *this* goroutine (no parallel disk I/O).
    go func() {
        bfsComputeSizes(rootPath, netMap, *slowFlag, dirChan, resultChan)
        // After BFS finishes, close channels so progress goroutine stops.
        close(dirChan)
        close(resultChan)
    }()

    // 6) Collect BFS results (in main goroutine), store them in a slice
    var allResults []FolderSize
    for fs := range resultChan {
        allResults = append(allResults, fs)
    }

    // Wait for progress goroutine to end
    <-doneChan
    // Clear the final progress line
    fmt.Printf("\r\033[K\n")

    // 7) Sort the BFS results by size and print the top N
    sort.Slice(allResults, func(i, j int) bool {
        return allResults[i].Size > allResults[j].Size
    })

    // Optionally print any detected network FS
    if len(netMounts) > 0 {
        fmt.Println("Network filesystems detected (skipped entirely):")
        for _, nm := range netMounts {
            fmt.Printf("  %s => %s\n", nm.DisplayName, nm.MountPoint)
        }
        fmt.Println()
    }

    // Print the top X largest directories
    fmt.Printf("Top %d largest directories under '%s':\n", *topFlag, rootPath)
    w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)

    count := 0
    for _, fs := range allResults {
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

