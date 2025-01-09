// File: find-large-dirs.go
//
// A single-file BFS directory scanner that compiles on all Go platforms:
//  - No "syscall" references, no OS-specific calls.
//  - Shows immediate, colored progress and partial results on interrupt.
//  - Skips any duplication or network FS detection to remain universal.
//  - NEW: Calculates file-type proportions (e.g., 20% Images, 30% Video, etc.),
//         now color-coded by category.
//
// Usage: find-large-dirs [directory] [--top <N>] [--slow-threshold <duration>]
//                        [--exclude <path>] (repeatable)
//                        [--help] [--version]
//
// Default: shows top 15 largest directories.
//
// Example: find-large-dirs /home --top 20 --exclude /home/user/cache --slow-threshold 3s
//
// Author: github.com/matveynator

package main

import (
	"container/list"
	"context"
	"encoding/json" // <- ADDED for JSON storage
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

var version = "dev" // Can be set at build time.

// FolderSize holds info about each directory: path, size, skip-flag, file-type size map.
type FolderSize struct {
	Path      string
	Size      int64
	Skipped   bool
	FileTypes map[string]int64
}

// progressUpdate is sent to the progressReporter goroutine to display scanning progress.
type progressUpdate struct {
	CurrentDir string
	NumDirs    int64
	BytesTotal int64
}

// multiFlag is used to support multiple --exclude flags.
type multiFlag []string

func (m *multiFlag) String() string {
	return strings.Join(*m, ", ")
}
func (m *multiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

/*
   ------------------------------------------------------------------------------------
   ANSI COLOR CODES (including a new function for category-based colors)
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
)

// getColorForCategory returns an ANSI color code based on the file category name.
// You can customize this mapping as you wish.
func getColorForCategory(cat string) string {
	switch cat {
	case "Image":
		return ColorGreen
	case "Video":
		return ColorMagenta
	case "Audio":
		return ColorCyan
	case "Archive":
		return ColorRed
	case "Document":
		return ColorYellow
	case "Application":
		return ColorBlue
	case "Code":
		return ColorBlue
	case "Log":
		return ColorRed
	case "Database":
		return ColorMagenta
	case "Backup":
		return ColorRed
	case "Disk Image":
		return ColorCyan
	case "Configuration":
		return ColorYellow
	case "Font":
		return ColorCyan
	case "Web":
		return ColorGreen
	case "Spreadsheet":
		return ColorMagenta
	case "Presentation":
		return ColorBlue
	default:
		return ColorReset
	}
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

// shortenPath truncates a path for display if it exceeds maxLen, appending "..." at the end.
func shortenPath(path string, maxLen int) string {
	if len(path) <= maxLen {
		return path
	}
	return path[:maxLen-3] + "..."
}

// isExcluded checks whether a given path should be skipped by userExcludes or system directories.
func isExcluded(path string, userExcludes []string) bool {
	// Check user-provided excludes.
	for _, ex := range userExcludes {
		if strings.HasPrefix(path, ex) {
			return true
		}
	}
	// Check standard system/pseudo directories.
	base := filepath.Base(path)
	switch strings.ToLower(base) {
	case "proc", "sys", "dev", "run", "tmp", "var":
		return true
	default:
		return false
	}
}

// classifyExtension groups files by common extensions: Image, Video, Audio, Archive, etc.
func classifyExtension(fileName string) string {
	ext := strings.ToLower(filepath.Ext(fileName))

	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".tiff", ".raw", ".webp", ".heic", ".heif":
		return "Image"
	case ".mp4", ".mov", ".avi", ".mkv", ".flv", ".wmv", ".webm", ".m4v":
		return "Video"
	case ".mp3", ".wav", ".flac", ".aac", ".ogg", ".m4a", ".wma":
		return "Audio"
	case ".zip", ".rar", ".7z", ".tar", ".gz", ".bz2", ".xz":
		return "Archive"
	case ".pdf", ".doc", ".docx", ".txt", ".rtf":
		return "Document"
	case ".exe", ".dll", ".so", ".bin", ".dmg", ".pkg", ".apk":
		return "Application"
	case ".go", ".c", ".cpp", ".h", ".hpp", ".js", ".ts", ".py", ".java", ".sh", ".rb", ".php":
		return "Code"
	case ".log", ".trace", ".log.gz", ".log.bz2":
		return "Log"
	case ".sql", ".db", ".sqlite", ".sqlite3", ".mdb", ".accdb", ".ndb", ".frm", ".ibd", ".myd", ".myi", ".rdb", ".aof", ".wal", ".shm", ".journal":
		return "Database"
	case ".bak", ".backup", ".bkp", ".ab", ".dump":
		return "Backup"
	case ".iso", ".img", ".vhd", ".vhdx", ".vmdk", ".dsk":
		return "Disk Image"
	case ".conf", ".cfg", ".ini", ".yaml", ".yml", ".json", ".xml", ".toml":
		return "Configuration"
	case ".ttf", ".otf", ".woff", ".woff2", ".eot":
		return "Font"
	case ".html", ".htm", ".css", ".scss", ".less":
		return "Web"
	case ".ods", ".xls", ".xlsx", ".csv":
		return "Spreadsheet"
	case ".odp", ".ppt", ".pptx":
		return "Presentation"
	default:
		return "Other"
	}
}

// formatFileTypeRatios builds a string like "20.00% Image, 30.00% Video, 50.00% Other"
// with categories in descending order of size, each category in its own color.
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

	// Sort by descending size.
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].Size > pairs[j].Size
	})

	var parts []string
	for _, p := range pairs {
		percent := float64(p.Size) / float64(totalSize) * 100
		catColor := getColorForCategory(p.Cat)

		parts = append(parts,
			fmt.Sprintf("%s%.2f%%%s %s%s%s",
				ColorGreen,         // color for the percentage value
				percent,
				ColorReset,
				catColor,           // color for the category text
				p.Cat,
				ColorReset,
			),
		)
	}

	return strings.Join(parts, ", ")
}

/*
   ------------------------------------------------------------------------------------
   FUNCTIONS TO LOAD/SAVE PREVIOUS DATA (JSON)
   ------------------------------------------------------------------------------------
   We store previous run results in the user's home directory: ~/.find-large-dirs/db.json
   It contains a list of (Path, Size) pairs. Then we can compute diffs at next run.
*/

type dbEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type dbData struct {
	Timestamp time.Time  `json:"timestamp"`
	Entries   []dbEntry  `json:"entries"`
}

// getDbPath returns the path to the JSON file in ~/.find-large-dirs/db.json
func getDbPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Fallback if for some reason we can't get the home directory
		return "./find-large-dirs-db.json"
	}
	return filepath.Join(homeDir, ".find-large-dirs", "db.json")
}

// loadPreviousData loads a map of path->size and the timestamp from the JSON file if it exists.
func loadPreviousData(dbPath string) (map[string]int64, time.Time, error) {
	data := make(map[string]int64)
	var timestamp time.Time

	file, err := os.Open(dbPath)
	if err != nil {
		// If no file yet, just return empty map and zero time, no error.
		return data, timestamp, nil
	}
	defer file.Close()

	var db dbData
	if err := json.NewDecoder(file).Decode(&db); err != nil {
		return data, timestamp, nil
	}

	for _, e := range db.Entries {
		data[e.Path] = e.Size
	}
	timestamp = db.Timestamp
	return data, timestamp, nil
}

// saveCurrentData saves the current path->size data and the current timestamp to the JSON file.
func saveCurrentData(dbPath string, folders []FolderSize) error {
	// Ensure the directory ~/.find-large-dirs/ exists
	if err := os.MkdirAll(filepath.Dir(dbPath), 0750); err != nil {
		return err
	}

	var entries []dbEntry
	for _, fs := range folders {
		entries = append(entries, dbEntry{Path: fs.Path, Size: fs.Size})
	}

	db := dbData{
		Timestamp: time.Now(),
		Entries:   entries,
	}

	out, err := os.Create(dbPath)
	if err != nil {
		return err
	}
	defer out.Close()

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(db)
}

/*
   ------------------------------------------------------------------------------------
   BFS SCANNING
   ------------------------------------------------------------------------------------
   We perform a single-threaded BFS to avoid overwhelming I/O. For each directory:
     - Sum up the sizes of the *files* (not subdirectories).
     - Categorize each file by type (Image, Video, etc.).
     - If scanning a directory exceeds 'slowThreshold', we mark that directory as Skipped.
     - We send progress updates to a separate goroutine via progChan for near real-time UI.
     - We do NOT detect duplicates or handle network FS specifics to remain universal.
*/

func bfsScan(
	ctx context.Context,
	root string,
	excludes []string,
	slowThreshold time.Duration,
	progChan chan<- progressUpdate,
) []FolderSize {

	results := make(map[string]*FolderSize)
	var dirCount int64
	var totalBytes int64

	queue := list.New()
	queue.PushBack(root)
	results[root] = &FolderSize{Path: root, FileTypes: make(map[string]int64)}

BFSLOOP:
	for queue.Len() > 0 {
		select {
		case <-ctx.Done():
			break BFSLOOP
		default:
		}

		e := queue.Front()
		queue.Remove(e)
		dirPath := e.Value.(string)

		if isExcluded(dirPath, excludes) {
			res := ensureFolder(results, dirPath)
			res.Skipped = true
			continue
		}

		start := time.Now()
		var localSize int64
		skipThis := false

		files, err := ioutil.ReadDir(dirPath)
		if err != nil {
			skipThis = true
		} else {
			for _, fi := range files {
				if time.Since(start) > slowThreshold {
					skipThis = true
					break
				}
				if !fi.IsDir() {
					size := fi.Size()
					localSize += size
					cat := classifyExtension(fi.Name())
					fEntry := ensureFolder(results, dirPath)
					fEntry.FileTypes[cat] += size
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

		progChan <- progressUpdate{
			CurrentDir: dirPath,
			NumDirs:    atomic.LoadInt64(&dirCount),
			BytesTotal: atomic.LoadInt64(&totalBytes),
		}

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
   Reads progress updates from progChan and prints them every 300ms, until the channel
   is closed or the context is canceled (e.g. user interrupt).
*/

func progressReporter(ctx context.Context, progChan <-chan progressUpdate, done chan<- bool) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	var last progressUpdate

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("\r\033[K")
			done <- true
			return

		case upd, ok := <-progChan:
			if !ok {
				fmt.Printf("\r\033[K")
				done <- true
				return
			}
			last = upd

		case <-ticker.C:
			fmt.Printf("\r\033[K") // Clear the line

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
   1) Parse flags (help, version, top, slow-threshold, exclude) in any order.
   2) Decide the root directory ("/" or "." if "/" doesn't exist).
   3) Set up SIGINT interrupt handling to gracefully stop scanning.
   4) Launch a goroutine for progressReporter.
   5) BFS-scan in the main goroutine.
   6) Print top N largest directories with file-type proportions (each category colored).
   7) NEW: Load previous data, display diffs for top directories, save new data.
*/

func main() {
	flag.Usage = func() {
		fmt.Println("Usage: find-large-dirs [directory] [--top <N>] [--slow-threshold <duration>]")
		fmt.Println("                         [--exclude <path>] (repeatable)")
		fmt.Println("                         [--help] [--version]")
		fmt.Println("")
		fmt.Println("Simple BFS across all subdirectories in one thread, prints immediate progress,")
		fmt.Println("partial results if interrupted, and shows file-type proportions in each directory.")
		fmt.Println("Compiles on all platforms without OS-specific calls.")
	}

	helpFlag := flag.Bool("help", false, "Show this help message")
	topFlag := flag.Int("top", 15, "Number of top-largest directories to display (default 15)")
	slowFlag := flag.Duration("slow-threshold", 2*time.Second,
		"Max time to scan a directory before skipping it (e.g., 2s, 500ms)")
	versFlag := flag.Bool("version", false, "Show version")

	var excludeFlag multiFlag
	flag.Var(&excludeFlag, "exclude", "Specify paths to ignore (repeatable)")

	flag.Parse()

	if *helpFlag {
		flag.Usage()
		return
	}
	if *versFlag {
		fmt.Printf("find-large-dirs version: %s\n", version)
		return
	}

	root := "/"
	if _, err := os.Stat(root); os.IsNotExist(err) {
		root = "."
	}
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	// Load previous data for comparison:
	dbPath := getDbPath()
	prevData, prevTimestamp, _ := loadPreviousData(dbPath)

	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	go func() {
		<-sigChan
		fmt.Fprintf(os.Stderr, "\nInterrupted. Finalizing...\n")
		cancel()
	}()

	fmt.Printf("Scanning '%s'...\n\n", root)

	progChan := make(chan progressUpdate, 10)
	doneChan := make(chan bool)
	go progressReporter(ctx, progChan, doneChan)

	folders := bfsScan(ctx, root, excludeFlag, *slowFlag, progChan)

	close(progChan)
	<-doneChan
	fmt.Println()

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

		// Calculate difference with previous run (if available)
		diffStr := ""
		if oldSize, found := prevData[fs.Path]; found && oldSize > 0 {
			diffMB := float64(fs.Size-oldSize) / (1 << 20)
			percent := 0.0
			if oldSize != 0 {
				percent = (diffMB / (float64(oldSize) / (1 << 20))) * 100
			}
			sign := "+"
			if diffMB < 0 {
				sign = ""
			}
			if (diffMB != 0) || (percent != 0.0){
				// Build a string like: (+12.34 MB, +5.67%)
				diffStr = fmt.Sprintf(" (%s%.2f MB, %s%.2f%%)", sign, diffMB, sign, percent)
			}
		}

		fmt.Fprintf(tw,
			"%-12s\t%s%s%s%s%s\n",
			formatSize(fs.Size),
			Bold, fs.Path, ColorReset, note, diffStr,
		)
		ratioStr := formatFileTypeRatios(fs.FileTypes, fs.Size)
		fmt.Fprintf(tw, "            \t -> File types: %s\n", ratioStr)

		count++
	}
	tw.Flush()

	// Show time elapsed since last scan
	if !prevTimestamp.IsZero() {
		elapsed := time.Since(prevTimestamp).Round(time.Second)
		fmt.Printf("\nTime since last scan: %s\n", elapsed)
	}

	// Save current data for next run
	if err := saveCurrentData(dbPath, folders); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: unable to save data to %s: %v\n", dbPath, err)
	}
}

