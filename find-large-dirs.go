package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"
)

// Version of the program, default to "dev".
var version = "dev"

// FolderSize stores the path and size of a directory, as well as whether it was skipped
// due to being too slow to process.
type FolderSize struct {
	Path    string
	Size    int64
	Skipped bool
}

// formatSize converts the size in bytes to a human-readable format (GB, MB, KB).
func formatSize(size int64) string {
	if size >= 1<<30 {
		return fmt.Sprintf("%.2f GB", float64(size)/(1<<30))
	} else if size >= 1<<20 {
		return fmt.Sprintf("%.2f MB", float64(size)/(1<<20))
	}
	return fmt.Sprintf("%d KB", size/(1<<10))
}

// isExcluded determines if a directory should be excluded from processing
// based on its name (e.g., system directories like /proc, /sys, /dev).
func isExcluded(path string) bool {
	base := filepath.Base(path)
	if base == "proc" || base == "sys" || base == "dev" {
		return true
	}
	return false
}

// calculateFolderSize calculates the total size of a folder. If the processing
// takes longer than the provided slowThreshold, it will stop and mark the folder as skipped.
func calculateFolderSize(path string, slowThreshold time.Duration) (int64, bool) {
	var totalSize int64
	start := time.Now()
	err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if info.IsDir() {
			if isExcluded(p) {
				return filepath.SkipDir
			}
		} else {
			totalSize += info.Size()
		}
		if time.Since(start) > slowThreshold {
			return fmt.Errorf("slow directory")
		}
		return nil
	})

	if err != nil {
		return totalSize, true
	}
	return totalSize, false
}

// countTotalItems counts the total number of directories within the provided path.
// This helps in accurately tracking progress.
func countTotalItems(path string) int32 {
	var count int32
	_ = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() {
			atomic.AddInt32(&count, 1)
		}
		return nil
	})
	return count
}

// findAllSubfolders scans the directory tree starting at the given path, calculates
// sizes for each subfolder, and tracks progress. Slow folders are skipped and marked.
func findAllSubfolders(path string, progress *int32, totalItems int32, slowThreshold time.Duration) []FolderSize {
	subfolders := []FolderSize{}
	_ = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if info.IsDir() {
			size, skipped := calculateFolderSize(p, slowThreshold)
			subfolders = append(subfolders, FolderSize{Path: p, Size: size, Skipped: skipped})
			atomic.AddInt32(progress, 1) // Update progress
		}
		return nil
	})

	sort.Slice(subfolders, func(i, j int) bool {
		return subfolders[i].Size > subfolders[j].Size
	})

	return subfolders
}

func main() {
	// Define command-line flags
	flag.Usage = func() {
		fmt.Println("Usage: find-large-dirs [directory] [--top <number>] [--help] [--version]")
		fmt.Println("Scan the directory and display disk usage in MB/GB.")
	}
	help := flag.Bool("help", false, "Show usage")
	top := flag.Int("top", 20, "Number of top folders to display")
	slowThreshold := flag.Duration("slow-threshold", 2*time.Second, "Threshold for considering a directory as slow")
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

	// Determine the path to scan (default to root if no argument is provided).
	path := "/"
	if flag.NArg() > 0 {
		path = flag.Arg(0)
	}

	// Count the total number of items to initialize progress tracking.
	totalItems := countTotalItems(path)
	fmt.Printf("Scanning directory: %s\n", path)
	var progress int32

	// Start a goroutine to display progress updates.
	go func() {
		for {
			percentage := float64(atomic.LoadInt32(&progress)) / float64(totalItems) * 100
			fmt.Printf("\rProgress: %.2f%%", percentage)
			time.Sleep(100 * time.Millisecond)
		}
	}()

	// Find all subfolders and their sizes.
	subfolders := findAllSubfolders(path, &progress, totalItems, *slowThreshold)
	fmt.Println() // Move to the next line after progress

	// Display the top N largest directories.
	fmt.Printf("\nTop %d Directories by Size:\n", *top)
	for i, folder := range subfolders {
		if i >= *top {
			break
		}
		status := ""
		if folder.Skipped {
			status = "(skipped)"
		}
		fmt.Printf("%s\t%s %s\n", formatSize(folder.Size), folder.Path, status)
	}
}

