// File: find-large-dirs.go
//
// A single-file BFS directory scanner that compiles on all Go platforms:
//  - No "syscall" references, no OS-specific calls.
//  - Shows immediate progress with ANSI color highlights, and partial results on interrupt.
//  - Skips any duplication or network FS detection to remain universal.
//  - NEW: Calculates file-type proportions (e.g., 20% Images, 30% Video, etc.)
//  - Default: shows top 15 largest directories.
//
// Usage: find-large-dirs [directory] [--top <N>] [--slow-threshold <duration>]
//                        [--exclude <path>] (repeatable)
//                        [--help] [--version]
//
// Example: find-large-dirs /home --top 20 --exclude /home/user/cache --slow-threshold 3s
//
// Author: github.com/matveynator

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
   ------------------------------------------------------------------------------------
   GLOBALS & DATA STRUCTURES
   ------------------------------------------------------------------------------------
*/

// version holds the program version, can be set at build time (default is "dev").
var version = "dev"

// FolderSize represents each folder found, including its path, the accumulated
// size of files *within that directory*, a Skipped flag if not fully scanned,
// and a map of file types to their total size (e.g. "Image" -> 12345 bytes).
type FolderSize struct {
	Path      string
	Size      int64
	Skipped   bool
	FileTypes map[string]int64
}

// progressUpdate is the structure sent through a channel to our progressReporter goroutine.
// This allows us to display the current directory being scanned, how many directories
// have been scanned, and the total accumulated bytes (only from non-skipped directories).
type progressUpdate struct {
	CurrentDir string
	NumDirs    int64
	BytesTotal int64
}

// multiFlag is a custom type that allows reading multiple --exclude flags.
// Each occurrence of --exclude <path> appends a path to this slice.
type multiFlag []string

// String returns the string representation of our multiFlag, joined by commas.
func (m *multiFlag) String() string {
	return strings.Join(*m, ", ")
}

// Set is called by the flag package when the --exclude flag is encountered.
func (m *multiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

/*
   ------------------------------------------------------------------------------------
   ANSI COLOR CODES (for optional highlighting)
   ------------------------------------------------------------------------------------
*/

const (
	ColorReset   = "\033[0m"
	ColorRed     = "\033[31m"
	ColorGreen   = "\033[32m"
	ColorYellow  = "\033[33m"
	ColorBlue    = "\033[34m"
	ColorMagenta = "\033[35m"
	ColorCyan    = "\033[36m"
	Bold         = "\033[1m"
	// You can add more colors or styles if desired
)

/*
   ------------------------------------------------------------------------------------
   HELPER UTILITIES
   ------------------------------------------------------------------------------------
*/

// formatSize converts a size in bytes to a human-readable KB/MB/GB string.
func formatSize(sz int64) string {
	switch {
	case sz >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(sz)/(1<<30))
	case sz >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(sz)/(1<<20))
	default:
		// Fallback for smaller sizes; displayed in KB.
		return fmt.Sprintf("%d KB", sz/(1<<10))
	}
}

// shortenPath truncates a path for display if it exceeds a specified maxLen,
// appending "..." at the end.
func shortenPath(path string, maxLen int) string {
	if len(path) <= maxLen {
		return path
	}
	return path[:maxLen-3] + "..."
}

// isExcluded checks whether a given path should be skipped, either because
// it matches a user-provided exclude prefix, or because it is one of the
// built-in system directories we choose to skip for universality.
func isExcluded(path string, userExcludes []string) bool {
	// First, check if the path matches one of the user excludes by prefix.
	for _, ex := range userExcludes {
		if strings.HasPrefix(path, ex) {
			return true
		}
	}

	// Then, skip certain standard system/pseudo directories for cross-platform safety.
	base := filepath.Base(path)
	switch strings.ToLower(base) {
	case "proc", "sys", "dev", "run", "tmp", "var":
		return true
	default:
		return false
	}
}

// classifyExtension categorizes a file based on its extension.
// We group popular extensions into categories like "Image", "Video", "Archive", etc.
// Anything not recognized falls under "Other".
func classifyExtension(fileName string) string {
	ext := strings.ToLower(filepath.Ext(fileName))

	switch ext {
	// Common image formats
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".tiff", ".raw", ".webp", ".heic", ".heif":
		return "Image"

	// Common video formats
	case ".mp4", ".mov", ".avi", ".mkv", ".flv", ".wmv", ".webm", ".m4v":
		return "Video"

	// Common audio formats
	case ".mp3", ".wav", ".flac", ".aac", ".ogg", ".m4a", ".wma":
		return "Audio"

	// Common archive/compressed formats
	case ".zip", ".rar", ".7z", ".tar", ".gz", ".bz2", ".xz":
		return "Archive"

	// Common documents
	case ".pdf", ".doc", ".docx", ".txt", ".rtf":
		return "Document"

	// Common executables/binaries (applications)
	case ".exe", ".dll", ".so", ".bin", ".dmg", ".pkg", ".apk":
		return "Application"

	// Common code/extensions
	case ".go", ".c", ".cpp", ".h", ".hpp", ".js", ".ts", ".py", ".java", ".sh", ".rb", ".php":
		return "Code"

	// Common log files (including rotated logs)
	case ".log", ".trace", ".dump", ".log.gz", ".log.bz2":
		return "Log"

	// Common database files
	case ".sql", ".db", ".sqlite", ".sqlite3", ".mdb", ".accdb", ".ndb", ".frm", ".ibd":
		return "Database"

	// Common backup files
	case ".bak", ".backup", ".bkp", ".ab":
		return "Backup"

	// Common disk image files
	case ".iso", ".img", ".vhd", ".vhdx", ".vmdk", ".dsk":
		return "Disk Image"

	// Common configuration files
	case ".conf", ".cfg", ".ini", ".yaml", ".yml", ".json", ".xml", ".toml":
		return "Configuration"

	// Common font files
	case ".ttf", ".otf", ".woff", ".woff2", ".eot":
		return "Font"

	// Common web files
	case ".html", ".htm", ".css", ".scss", ".less":
		return "Web"

	// Common spreadsheet formats
	case ".ods", ".xls", ".xlsx", ".csv":
		return "Spreadsheet"

	// Common presentation formats
	case ".odp", ".ppt", ".pptx":
		return "Presentation"

	// If not recognized, categorize as "Other"
	default:
		return "Other"
	}
}

// formatFileTypeRatios produces a summary string like "20.00% Image, 30.00% Video, 50.00% Other"
// given the map of file type sizes and the total directory size.
// We list categories in descending order of size contribution, with color highlighting.
func formatFileTypeRatios(fileTypes map[string]int64, totalSize int64) string {
	if totalSize == 0 {
		return "No files"
	}

	var pairs []struct {
		Cat  string
		Size int64
	}
	for cat, sz := range fileTypes {
		if sz > 0 {
			pairs = append(pairs, struct {
				Cat  string
				Size int64
			}{cat, sz})
		}
	}

	// Sort the pairs by descending size.
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].Size > pairs[j].Size
	})

	// Build a comma-separated string of "percent% Category" with some color.
	var parts []string
	for _, p := range pairs {
		percent := float64(p.Size) / float64(totalSize) * 100
		// We'll color the percentages in green for visual clarity.
		parts = append(parts, fmt.Sprintf("%s%.2f%%%s %s", 
			ColorGreen, percent, ColorReset, p.Cat))
	}

	return strings.Join(parts, ", ")
}

/*
   ------------------------------------------------------------------------------------
   BFS SCANNING
   ------------------------------------------------------------------------------------
   We perform a single-threaded BFS to avoid overwhelming I/O. For each directory:
     - We sum up sizes of the *files* within that directory only.
     - We also categorize each file by type (Image, Video, etc.).
     - If scanning takes longer than 'slowThreshold', we mark that directory as Skipped.
     - We send progress updates to a separate goroutine (progChan) for near real-time UI.
     - We do NOT detect duplicates or handle network FS specifics (to remain universal).
*/

// bfsScan performs a Breadth-First Search starting from 'root', excluding directories
// that match 'excludes'. If reading a directory takes longer than 'slowThreshold',
// that directory is marked as Skipped and its subdirectories are not scanned.
// Returns a slice of FolderSize sorted by descending folder size.
func bfsScan(
	ctx context.Context,
	root string,
	excludes []string,
	slowThreshold time.Duration,
	progChan chan<- progressUpdate,
) []FolderSize {

	// Map to store folder info by path. The key is the directory path, the value is FolderSize.
	results := make(map[string]*FolderSize)

	// We track how many directories we've processed and the total file bytes (for progress).
	var dirCount int64
	var totalBytes int64

	// The BFS queue holds directories to process.
	queue := list.New()
	queue.PushBack(root)
	// Ensure the root entry exists in results.
	results[root] = &FolderSize{Path: root, FileTypes: make(map[string]int64)}

BFSLOOP:
	for queue.Len() > 0 {
		// Check if the user canceled (e.g., via interrupt signal).
		select {
		case <-ctx.Done():
			// If context is canceled, break out of the BFS loop immediately.
			break BFSLOOP
		default:
		}

		// Pop the front of the queue (the next directory to scan).
		e := queue.Front()
		queue.Remove(e)
		dirPath := e.Value.(string)

		// If this directory is excluded, mark it as Skipped and do not scan further.
		if isExcluded(dirPath, excludes) {
			res := ensureFolder(results, dirPath)
			res.Skipped = true
			continue
		}

		// Start timing how long it takes to read the directory.
		start := time.Now()
		var localSize int64
		skipThis := false

		// Attempt to read the contents of the directory.
		files, err := ioutil.ReadDir(dirPath)
		if err != nil {
			// If there's any error (e.g., permission denied), mark as skipped.
			skipThis = true
		} else {
			// Iterate over each item in the directory.
			for _, fi := range files {
				// If it takes too long, skip the entire directory.
				if time.Since(start) > slowThreshold {
					skipThis = true
					break
				}
				// For each file, add its size to localSize and categorize it.
				if !fi.IsDir() {
					fileSize := fi.Size()
					localSize += fileSize
					fileCat := classifyExtension(fi.Name())
					fEntry := ensureFolder(results, dirPath)
					fEntry.FileTypes[fileCat] += fileSize
				}
			}
		}

		fEntry := ensureFolder(results, dirPath)
		fEntry.Size = localSize
		fEntry.Skipped = skipThis

		atomic.AddInt64(&dirCount, 1)
		if !skipThis {
			atomic.AddInt64(&totalBytes, localSize)
		}

		// Send a progress update to the progressReporter goroutine.
		progChan <- progressUpdate{
			CurrentDir: dirPath,
			NumDirs:    atomic.LoadInt64(&dirCount),
			BytesTotal: atomic.LoadInt64(&totalBytes),
		}

		// If not skipped and no error, enqueue subdirectories.
		if !skipThis && err == nil {
			for _, fi := range files {
				if fi.IsDir() {
					subPath := filepath.Join(dirPath, fi.Name())
					_ = ensureFolder(results, subPath)
					queue.PushBack(subPath)
				}
			}
		}
	}

	// Convert map results to a slice, then sort it by descending size.
	var out []FolderSize
	for _, fs := range results {
		out = append(out, *fs)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Size > out[j].Size
	})
	return out
}

// ensureFolder returns a FolderSize entry from the map if it exists, otherwise creates it.
func ensureFolder(m map[string]*FolderSize, path string) *FolderSize {
	fs, ok := m[path]
	if !ok {
		fs = &FolderSize{
			Path:      path,
			FileTypes: make(map[string]int64),
		}
		m[path] = fs
	}
	return fs
}

/*
   ------------------------------------------------------------------------------------
   PROGRESS REPORTER GOROUTINE
   ------------------------------------------------------------------------------------
   This function runs in its own goroutine, reading progressUpdate items from progChan.
   It prints a status update every ~300ms, until the channel is closed or context canceled.
*/

// progressReporter reads progress updates from progChan and prints them
// every 300ms. It exits when the BFS is done (channel closes) or user interrupts.
func progressReporter(ctx context.Context, progChan <-chan progressUpdate, done chan<- bool) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	var last progressUpdate

	for {
		select {
		case <-ctx.Done():
			// If the user interrupted, clear the line and exit.
			fmt.Printf("\r\033[K")
			done <- true
			return

		case upd, ok := <-progChan:
			if !ok {
				// If BFS ended (channel closed), clear line and exit.
				fmt.Printf("\r\033[K")
				done <- true
				return
			}
			// Store the most recent update to print on the next tick.
			last = upd

		case <-ticker.C:
			// Print the last known update in a single terminal line, with color/bold.
			fmt.Printf("\r\033[K") // Clear the current line.

			shortDir := shortenPath(last.CurrentDir, 40)
			fmt.Printf("%sScanning:%s %s%-40s%s | %sDirs:%s %d | %sSize:%s %s",
				ColorCyan, ColorReset,
				Bold, shortDir, ColorReset,
				ColorYellow, ColorReset, last.NumDirs,
				ColorGreen, ColorReset, formatSize(last.BytesTotal),
			)
		}
	}
}

/*
   ------------------------------------------------------------------------------------
   MAIN FUNCTION
   ------------------------------------------------------------------------------------
   1) Parse command-line flags (help, version, top, slow-threshold, exclude).
   2) Determine root directory to scan (default "/" or "." if "/" doesn't exist).
   3) Set up context cancellation on interrupt (SIGINT).
   4) Start the progressReporter goroutine.
   5) Perform BFS scan in the main goroutine.
   6) On completion, print the top N largest directories, including file-type proportions.
*/

func main() {
	// Define custom usage instructions.
	flag.Usage = func() {
		fmt.Println("Usage: find-large-dirs [directory] [--top <N>] [--slow-threshold <duration>]")
		fmt.Println("                         [--exclude <path>] (repeatable)")
		fmt.Println("                         [--help] [--version]")
		fmt.Println("")
		fmt.Println("Simple BFS across all subdirectories in one thread, prints immediate progress,")
		fmt.Println("and partial results if interrupted. No OS-specific calls. Compiles on all platforms.")
		fmt.Println("Also shows file-type proportions in each directory.")
	}

	helpFlag := flag.Bool("help", false, "Show this help message")
	topFlag := flag.Int("top", 15, "How many top-largest directories to display (default 15)")
	slowFlag := flag.Duration("slow-threshold", 2*time.Second,
		"Max time to scan a single directory before skipping it (e.g., 2s, 500ms)")
	versFlag := flag.Bool("version", false, "Show version")

	var excludeFlag multiFlag
	flag.Var(&excludeFlag, "exclude", "Specify paths to ignore (repeatable)")

	// Parse flags (works in any order).
	flag.Parse()

	// If --help was requested, print usage and exit.
	if *helpFlag {
		flag.Usage()
		return
	}

	// If --version was requested, print version and exit.
	if *versFlag {
		fmt.Printf("find-large-dirs version: %s\n", version)
		return
	}

	// Determine the root directory to scan. Default: "/" or "." if "/" doesn't exist.
	root := "/"
	if _, err := os.Stat(root); os.IsNotExist(err) {
		root = "."
	}
	// If user provided a directory argument, use that instead.
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	// Set up context cancellation upon receiving interrupt (SIGINT).
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	go func() {
		<-sigChan
		fmt.Fprintf(os.Stderr, "\nInterrupted. Finalizing...\n")
		cancel()
	}()

	// Inform the user which directory is being scanned.
	fmt.Printf("Scanning '%s'...\n\n", root)

	// Start the progress reporter goroutine.
	progChan := make(chan progressUpdate, 10)
	doneChan := make(chan bool)
	go progressReporter(ctx, progChan, doneChan)

	// Perform the BFS scan in the main goroutine.
	folders := bfsScan(ctx, root, excludeFlag, *slowFlag, progChan)

	// Close the progress channel and wait for the reporter to finish.
	close(progChan)
	<-doneChan
	fmt.Println()

	// Print the top N largest directories.
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

		// Print each directory's size, path (in bold), note, and file-type proportions.
		fmt.Fprintf(tw,
			"%-12s\t%s%s%s%s\n",
			formatSize(fs.Size),
			Bold, fs.Path, ColorReset, note,
		)
		ratioStr := formatFileTypeRatios(fs.FileTypes, fs.Size)
		fmt.Fprintf(tw, "            \t -> File types: %s\n", ratioStr)

		count++
	}
	tw.Flush()
}

