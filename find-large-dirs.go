// File: find-large-dirs.go
//
// A single-file BFS scanner that compiles on *all* Go platforms.
// - No "syscall" references, no OS-specific calls.
// - Shows immediate progress, partial results on interrupt (where signals exist).
// - Skips any duplication or network FS detection to remain universal.

package main

import (
    "container/list"
    "context"
    "flag"
    "fmt"
    "io/ioutil"
    "os"
    "os/signal"
    "path/filepath"
    "sort"
    "strings"
    "sync/atomic"
    "text/tabwriter"
    "time"
)

/*
   GLOBALS & DATA STRUCTURES
*/

// Program version (can be set at build time, or default "dev")
var version = "dev"

// FolderSize holds info about each folder: path + accumulated file size.
type FolderSize struct {
    Path    string
    Size    int64
    Skipped bool
}

// progressUpdate is what we send through a channel to the progressReporter goroutine.
type progressUpdate struct {
    CurrentDir string
    NumDirs    int64
    BytesTotal int64
}

/*
   UTILITIES
*/

// formatSize converts bytes to a simple KB/MB/GB string.
func formatSize(sz int64) string {
    switch {
    case sz >= 1<<30:
        return fmt.Sprintf("%.2f GB", float64(sz)/(1<<30))
    case sz >= 1<<20:
        return fmt.Sprintf("%.2f MB", float64(sz)/(1<<20))
    default:
        return fmt.Sprintf("%d KB", sz/(1<<10))
    }
}

// shortenPath truncates a path for display so it won't overflow the terminal line.
func shortenPath(path string, maxLen int) string {
    if len(path) <= maxLen {
        return path
    }
    return path[:maxLen-3] + "..."
}

// isExcluded checks if we want to skip certain directories (e.g., /proc).
// Because we want maximum portability, we skip only a small default set.
func isExcluded(path string) bool {
    // For universal usage, let's skip only well-known pseudo-dirs on Unix.
    // If you're on a system where these don't exist, no harm done.
    base := filepath.Base(path)
    switch strings.ToLower(base) {
    case "proc", "sys", "dev", "run", "tmp", "var":
        return true
    default:
        return false
    }
}

/*
   BFS
   We do a single-thread BFS so we don't overload IO. For each directory:
   - Sum up the sizes of *files* in that directory. (No duplicates detection.)
   - If scanning the directory takes too long (over slowThreshold), skip it entirely.
   - Send progress to a channel so a separate goroutine can print it immediately.
*/

// bfsScan walks the tree from root, returns slice of FolderSize sorted by descending size.
func bfsScan(ctx context.Context, root string, slowThreshold time.Duration, progChan chan<- progressUpdate) []FolderSize {
    results := make(map[string]*FolderSize)

    // We'll track how many directories we've processed + total file bytes.
    var dirCount int64
    var totalBytes int64

    queue := list.New()
    queue.PushBack(root)
    results[root] = &FolderSize{Path: root}

BFSLOOP:
    for queue.Len() > 0 {
        // Check if user canceled (interrupt)
        select {
        case <-ctx.Done():
            break BFSLOOP
        default:
        }

        e := queue.Front()
        queue.Remove(e)
        dirPath := e.Value.(string)

        // Possibly skip certain system directories
        if isExcluded(dirPath) {
            res := ensureFolder(results, dirPath)
            res.Skipped = true
            continue
        }

        start := time.Now()
        var localSize int64
        skipThis := false

        // Read directory contents
        files, err := ioutil.ReadDir(dirPath)
        if err != nil {
            skipThis = true
        } else {
            for _, fi := range files {
                if time.Since(start) > slowThreshold {
                    skipThis = true
                    break
                }
                // Add file size
                if !fi.IsDir() {
                    localSize += fi.Size()
                }
            }
        }

        // Update the results map
        fEntry := ensureFolder(results, dirPath)
        fEntry.Size = localSize
        fEntry.Skipped = skipThis

        atomic.AddInt64(&dirCount, 1)
        if !skipThis {
            atomic.AddInt64(&totalBytes, localSize)
        }

        // Send progress to progress goroutine
        progChan <- progressUpdate{
            CurrentDir: dirPath,
            NumDirs:    atomic.LoadInt64(&dirCount),
            BytesTotal: atomic.LoadInt64(&totalBytes),
        }

        // If not skipped, enqueue subdirectories
        if !skipThis && err == nil {
            for _, fi := range files {
                if fi.IsDir() {
                    subPath := filepath.Join(dirPath, fi.Name())
                    // Make sure we have an entry
                    if _, ok := results[subPath]; !ok {
                        results[subPath] = &FolderSize{Path: subPath}
                    }
                    queue.PushBack(subPath)
                }
            }
        }
    }

    // Convert map => slice
    var out []FolderSize
    for _, fs := range results {
        out = append(out, *fs)
    }
    // Sort by descending size
    sort.Slice(out, func(i, j int) bool {
        return out[i].Size > out[j].Size
    })
    return out
}

// ensureFolder is a small helper to get or create a FolderSize entry.
func ensureFolder(m map[string]*FolderSize, path string) *FolderSize {
    fs, ok := m[path]
    if !ok {
        fs = &FolderSize{Path: path}
        m[path] = fs
    }
    return fs
}

/*
   PROGRESS GOROUTINE
   Prints an update every 250-300ms until the channel is closed or context canceled.
*/

func progressReporter(ctx context.Context, progChan <-chan progressUpdate, done chan<- bool) {
    ticker := time.NewTicker(300 * time.Millisecond)
    defer ticker.Stop()

    var last progressUpdate

    for {
        select {
        case <-ctx.Done():
            // user interrupted => finalize
            fmt.Printf("\r\033[K")
            done <- true
            return
        case upd, ok := <-progChan:
            if !ok {
                // BFS ended => channel closed
                fmt.Printf("\r\033[K")
                done <- true
                return
            }
            last = upd
        case <-ticker.C:
            // Print the last update
            fmt.Printf("\r\033[K") // clear line
            shortDir := shortenPath(last.CurrentDir, 50)
            fmt.Printf(" Scanned dirs: %d | Accumulated size: %s | scanning: %s",
                last.NumDirs,
                formatSize(last.BytesTotal),
                shortDir,
            )
        }
    }
}

/*
   MAIN
   - We parse flags
   - Attempt to handle signals (if available)
   - BFS in main goroutine
   - Progress in separate goroutine
   - Print partial results if interrupted
*/

func main() {
    flag.Usage = func() {
        fmt.Println("Usage: find-large-dirs [directory] [--top <N>] [--slow-threshold <duration>]")
        fmt.Println(" [--help] [--version]")
        fmt.Println("Simple BFS across all subdirectories in one thread, prints immediate progress,")
        fmt.Println("and partial results if interrupted. No OS-specific calls. Compiles on *all* platforms.")
    }
    helpFlag := flag.Bool("help", false, "Show help")
    topFlag := flag.Int("top", 30, "How many top-largest directories to display")
    slowFlag := flag.Duration("slow-threshold", 2*time.Second, "Max time to scan a directory before skipping it")
    versFlag := flag.Bool("version", false, "Show version")
    flag.Parse()

    if *helpFlag {
        flag.Usage()
        return
    }
    if *versFlag {
        fmt.Printf("find-large-dirs version: %s\n", version)
        return
    }

    // Default root: "/" on Unix-likes, but some OS might not have a root path.
    // We'll fallback to "." if "/" doesn't exist or if user didn't specify.
    root := "/"
    // On Windows, "/" is valid but is actually the root of current drive in many shells.
    // We'll do a quick check to see if it exists. If not, fallback to "."
    if _, err := os.Stat(root); err != nil && os.IsNotExist(err) {
        root = "."
    }
    // If user gave an argument, override
    if flag.NArg() > 0 {
        root = flag.Arg(0)
    }

    // Set up cancellation via signals (in most OS).  
    ctx, cancel := context.WithCancel(context.Background())

    // Some OS do not support signals well (js/wasm, etc.). If it fails, you can remove.
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, os.Interrupt) // SIGINT
    // No reference to syscalls or platform-specific signals beyond this
    go func() {
        <-sigChan
        fmt.Fprintf(os.Stderr, "\nInterrupted. Finalizing...\n")
        cancel()
    }()

    fmt.Printf("Scanning '%s'...\n\n", root)

    // Start progress goroutine
    progChan := make(chan progressUpdate, 10)
    doneChan := make(chan bool)
    go progressReporter(ctx, progChan, doneChan)

    // Run BFS in main goroutine
    folders := bfsScan(ctx, root, *slowFlag, progChan)

    // Close progress channel, wait for progress goroutine
    close(progChan)
    <-doneChan
    fmt.Println()

    // Print top N largest directories
    fmt.Printf("Top %d largest directories in '%s':\n", *topFlag, root)
    tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
    count := 0
    for _, fs := range folders {
        if count >= *topFlag {
            break
        }
        note := ""
        if fs.Skipped {
            note = " (skipped)"
        }
        fmt.Fprintf(tw, "%-10s\t%s%s\n", formatSize(fs.Size), fs.Path, note)
        count++
    }
    tw.Flush()
}

