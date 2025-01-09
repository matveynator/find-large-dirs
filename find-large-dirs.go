// File: find-large-dirs.go
//
// A single-file BFS scanner that compiles on all Go platforms:
// - No "syscall" references, no OS-specific calls.
// - Shows immediate progress, and partial results on interrupt (if signals are supported).
// - Skips any duplication or network FS detection to stay universal.
// - NEW: Calculates file-type proportions (e.g., 20% Images, 30% Video, etc.)

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
// and a map of file types to their total size for the directory (e.g. "Image" -> total bytes).
type FolderSize struct {
	Path      string
	Size      int64
	Skipped   bool
	FileTypes map[string]int64 // e.g. {"Image": 12345, "Video": 67890, ...}
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
	// If path starts with any user exclude, we skip it.
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
// We list categories in descending order of size contribution.
func formatFileTypeRatios(fileTypes map[string]int64, totalSize int64) string {
	if totalSize == 0 {
		return "No files"
	}

	// Create a slice of (category, size) pairs from the map.
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

	// Build a comma-separated string of "percent% Category"
	var parts []string
	for _, p := range pairs {
		percent := float64(p.Size) / float64(totalSize) * 100
		parts = append(parts, fmt.Sprintf("%.2f%% %s", percent, p.Cat))
	}

	return strings.Join(parts, ", ")
}

/*
   ------------------------------------------------------------------------------------
   BFS SCANNING
   ------------------------------------------------------------------------------------
   We perform a single-threaded BFS to avoid overwhelming I/O. For each directory:
     - We sum up sizes of the *files* within that directory only.
     - We also categorize each file by type (Image, Video, etc.) for that directory.
     - If scanning takes longer than 'slowThreshold', we mark that directory as Skipped.
     - We send progress updates to a separate goroutine through progChan so it can
       display progress in near real-time.
     - We do NOT detect duplicates or handle network FS specifics (to remain universal).
*/

// bfsScan performs a Breadth-First Search starting from 'root', excluding directories
// that match 'excludes'. If reading a directory takes longer than 'slowThreshold',
// that directory is marked as Skipped and its subdirectories are also not scanned.
//
// This function returns a slice of FolderSize sorted by descending folder size.
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

	// The BFS queue holds directories we still need to process.
	queue := list.New()
	queue.PushBack(root)
	// Make sure the root entry exists in results.
	results[root] = &FolderSize{Path: root, FileTypes: make(map[string]int64)}

BFSLOOP:
	for queue.Len() > 0 {
		// Check if the user canceled (e.g., via interrupt signal).
		select {
		case <-ctx.Done():
			// If context is canceled, break out of the BFS loop immediately.
			break BFSLOOP
		default:
			// Otherwise, continue with BFS.
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
			// If there's any error (e.g., permission denied), mark this directory as skipped.
			skipThis = true
		} else {
			// We iterate over each item in the directory.
			for _, fi := range files {
				// If it takes too long (slower than slowThreshold), we skip the entire directory.
				if time.Since(start) > slowThreshold {
					skipThis = true
					break
				}
				// For each *file*, add its size to the localSize and update the file-type map.
				if !fi.IsDir() {
					fileSize := fi.Size()
					localSize += fileSize
					// Classify the file extension.
					fileCat := classifyExtension(fi.Name())
					fEntry := ensureFolder(results, dirPath)
					fEntry.FileTypes[fileCat] += fileSize
				}
			}
		}

		// Update the results map with the folder's size and whether it was skipped.
		fEntry := ensureFolder(results, dirPath)
		fEntry.Size = localSize
		fEntry.Skipped = skipThis

		// Atomically increment the directory count.
		atomic.AddInt64(&dirCount, 1)
		// If not skipped, add this local size to the totalBytes.
		if !skipThis {
			atomic.AddInt64(&totalBytes, localSize)
		}

		// Send a progress update to the progress reporter goroutine.
		progChan <- progressUpdate{
			CurrentDir: dirPath,
			NumDirs:    atomic.LoadInt64(&dirCount),
			BytesTotal: atomic.LoadInt64(&totalBytes),
		}

		// If this directory was not skipped and we had no error, enqueue its subdirectories.
		if !skipThis && err == nil {
			for _, fi := range files {
				if fi.IsDir() {
					subPath := filepath.Join(dirPath, fi.Name())
					// Ensure the sub-directory entry is in the results map.
					_ = ensureFolder(results, subPath)
					// Add the sub-directory to the BFS queue.
					queue.PushBack(subPath)
				}
			}
		}
	}

	// Convert the map of results to a slice, for sorting and final output.
	var out []FolderSize
	for _, fs := range results {
		out = append(out, *fs)
	}

	// Sort the folders by descending size.
	sort.Slice(out, func(i, j int) bool {
		return out[i].Size > out[j].Size
	})
	return out
}

// ensureFolder is a helper function that returns a FolderSize entry from the map
// if it exists, otherwise creates a new one in the map.
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
   The progressReporter function runs in its own goroutine, reading progressUpdate
   items from a channel. It prints a status update every ~300ms, until the channel
   is closed or the context is canceled.
*/

// progressReporter reads progress updates from progChan and prints them
// every 300ms. It exits when the BFS is done (channel closes) or when the user
// interrupts (ctx canceled).
func progressReporter(ctx context.Context, progChan <-chan progressUpdate, done chan<- bool) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	var last progressUpdate

	for {
		select {
		case <-ctx.Done():
			// If the user interrupted, clear the line and signal done.
			fmt.Printf("\r\033[K")
			done <- true
			return

		case upd, ok := <-progChan:
			if !ok {
				// If the BFS ended (channel closed), clear line and signal done.
				fmt.Printf("\r\033[K")
				done <- true
				return
			}
			// Store the most recent update so we can print it on the next tick.
			last = upd

		case <-ticker.C:
			// Every ~300ms, we print the last known update (if any).
			fmt.Printf("\r\033[K") // Clear the current terminal line.
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

// main is the entry point of our program.
func main() {
	// Define custom usage instructions.
	flag.Usage = func() {
		fmt.Println("Usage: find-large-dirs [directory] [--top <N>] [--slow-threshold <duration>]")
		fmt.Println("                         [--exclude <path>] (repeatable)")
		fmt.Println("                         [--help] [--version]")
		fmt.Println("")
		fmt.Println("Simple BFS across all subdirectories in one thread, prints immediate progress,")
		fmt.Println("and partial results if interrupted. No OS-specific calls. Compiles on all platforms.")
		fmt.Println("Now also shows file-type proportions in each directory.")
	}

	// Define command-line flags.
	helpFlag := flag.Bool("help", false, "Show help")
	topFlag := flag.Int("top", 30, "How many top-largest directories to display")
	slowFlag := flag.Duration("slow-threshold", 2*time.Second, "Max time to scan a directory before skipping it")
	versFlag := flag.Bool("version", false, "Show version")

	// multiFlag for handling multiple --exclude flags.
	var excludeFlag multiFlag
	flag.Var(&excludeFlag, "exclude", "Specify paths to ignore (repeatable)")

	// Parse the flags.
	flag.Parse()

	// If --help was requested, print the usage and exit.
	if *helpFlag {
		flag.Usage()
		return
	}

	// If --version was requested, print the version and exit.
	if *versFlag {
		fmt.Printf("find-large-dirs version: %s\n", version)
		return
	}

	// Determine the root directory to start scanning.
	// By default, we use "/", but if "/" doesn't exist, we use ".".
	root := "/"
	if _, err := os.Stat(root); err != nil && os.IsNotExist(err) {
		root = "."
	}
	// If the user provided a directory argument, use that instead.
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	// Set up context cancellation upon receiving an interrupt (SIGINT).
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	go func() {
		<-sigChan
		fmt.Fprintf(os.Stderr, "\nInterrupted. Finalizing...\n")
		cancel()
	}()

	// Inform the user what we are scanning.
	fmt.Printf("Scanning '%s'...\n\n", root)

	// Start the progress reporter goroutine.
	progChan := make(chan progressUpdate, 10)
	doneChan := make(chan bool)
	go progressReporter(ctx, progChan, doneChan)

	// Perform the BFS scan in the main goroutine.
	folders := bfsScan(ctx, root, excludeFlag, *slowFlag, progChan)

	// Close the progress channel and wait for the progressReporter to finish.
	close(progChan)
	<-doneChan
	fmt.Println()

	// Print out the top N largest directories.
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

		// Print each directory's size, path, note, and then the file-type proportions.
		// We indent the proportions line for clarity.
		fmt.Fprintf(tw, "%-10s\t%s%s\n", formatSize(fs.Size), fs.Path, note)
		ratioStr := formatFileTypeRatios(fs.FileTypes, fs.Size)
		fmt.Fprintf(tw, "          \t -> File types: %s\n", ratioStr)

		count++
	}
	tw.Flush()
}
