//find-large-dirs

package main

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var version = "v2.1"

type FolderSize struct {
	Path      string           `json:"path"`
	Size      int64            `json:"size_bytes"`
	Total     int64            `json:"total_bytes"`
	FileCount int64            `json:"file_count"`
	Oldest    time.Time        `json:"oldest_mtime"`
	Newest    time.Time        `json:"newest_mtime"`
	Skipped   bool             `json:"skipped"`
	FileTypes map[string]int64 `json:"types_bytes"`
}

type progressUpdate struct {
	CurrentDir string
	NumDirs    int64
	BytesTotal int64
}

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

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

func getColorForCategory(c string) string {
	switch c {
	case "Image":
		return ColorYellow
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
	case "DB-Backup":
		return ColorYellow
	case "Backup":
		return ColorRed
	case "Disk Image":
		return ColorCyan
	case "Configuration":
		return ColorYellow
	case "Font":
		return ColorCyan
	case "Web":
		return ColorYellow
	case "Spreadsheet":
		return ColorMagenta
	case "Presentation":
		return ColorBlue
	default:
		return ColorReset
	}
}

func formatSize(b int64) string {
	switch {
	case b >= 1<<40:
		return fmt.Sprintf("%.2f TB", float64(b)/(1<<40))
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func shortenPath(p string, n int) string {
	if len(p) <= n {
		return p
	}
	return p[:n-3] + "..."
}

func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	re := regexp.MustCompile(`^([0-9]*\.?[0-9]+)\s*([KMGTP]?)B?$`)
	m := re.FindStringSubmatch(s)
	if m == nil {
		return 0, errors.New("bad size")
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	mult := int64(1)
	switch m[2] {
	case "K":
		mult = 1 << 10
	case "M":
		mult = 1 << 20
	case "G":
		mult = 1 << 30
	case "T":
		mult = 1 << 40
	case "P":
		mult = 1 << 50
	}
	return int64(v * float64(mult)), nil
}

func isExcluded(p string, ex []string) bool {
	for _, e := range ex {
		if strings.HasPrefix(p, e) {
			return true
		}
	}
	switch strings.ToLower(filepath.Base(p)) {
	case "proc", "sys", "dev", "run", "tmp", "var":
		return true
	default:
		return false
	}
}

func classifyExtension(n string) string {
	switch strings.ToLower(filepath.Ext(n)) {
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
	case ".log", ".trace":
		return "Log"
	case ".db", ".sqlite", ".sqlite3", ".rdb":
		return "Database"
	case ".bak", ".backup":
		return "Backup"
	case ".sql":
		return "DB-Backup"
	case ".iso", ".img", ".vhd", ".vhdx", ".vmdk":
		return "Disk Image"
	case ".conf", ".cfg", ".ini", ".yaml", ".yml", ".json", ".xml":
		return "Configuration"
	case ".ttf", ".otf", ".woff":
		return "Font"
	case ".html", ".htm", ".css":
		return "Web"
	case ".ods", ".xls", ".xlsx", ".csv":
		return "Spreadsheet"
	case ".odp", ".ppt", ".pptx":
		return "Presentation"
	default:
		return "Other"
	}
}

func formatFileTypeRatios(m map[string]int64, total int64) string {
	if total == 0 {
		return "empty"
	}
	type pair struct {
		C string
		S int64
	}
	var ps []pair
	for c, s := range m {
		if s > 0 {
			ps = append(ps, pair{c, s})
		}
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].S > ps[j].S })
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, fmt.Sprintf("%s%.1f%%%s %s%s%s", ColorGreen, float64(p.S)*100/float64(total), ColorReset, getColorForCategory(p.C), p.C, ColorReset))
	}
	return strings.Join(out, ", ")
}

type dbEntry struct {
	Path string `json:"path"`
	Sz   int64  `json:"size"`
}
type dbData struct {
	Timestamp time.Time `json:"timestamp"`
	Entries   []dbEntry `json:"entries"`
}

func dbPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "./find-large-dirs-db.json"
	}
	return filepath.Join(home, ".find-large-dirs", "db.json")
}

func loadPrev(p string) (map[string]int64, time.Time) {
	m := map[string]int64{}
	f, err := os.Open(p)
	if err != nil {
		return m, time.Time{}
	}
	defer f.Close()
	var db dbData
	if json.NewDecoder(f).Decode(&db) != nil {
		return m, time.Time{}
	}
	for _, e := range db.Entries {
		m[e.Path] = e.Sz
	}
	return m, db.Timestamp
}

func saveCurrent(p string, m map[string]*FolderSize) {
	_ = os.MkdirAll(filepath.Dir(p), 0o750)
	f, err := os.Create(p)
	if err != nil {
		return
	}
	defer f.Close()
	db := dbData{Timestamp: time.Now()}
	for _, fs := range m {
		db.Entries = append(db.Entries, dbEntry{fs.Path, fs.Total})
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	_ = enc.Encode(db)
}

func bfsScan(ctx context.Context, root string, excl []string, slow time.Duration, prog chan<- progressUpdate) map[string]*FolderSize {
	res := map[string]*FolderSize{}
	ensure := func(p string) *FolderSize {
		if fs, ok := res[p]; ok {
			return fs
		}
		fs := &FolderSize{Path: p, FileTypes: map[string]int64{}}
		res[p] = fs
		return fs
	}
	q := list.New()
	q.PushBack(root)
	var dirCnt, bytesTotal int64
scan:
	for q.Len() > 0 {
		select {
		case <-ctx.Done():
			break scan
		default:
		}
		e := q.Front()
		q.Remove(e)
		dir := e.Value.(string)
		if isExcluded(dir, excl) {
			ensure(dir).Skipped = true
			continue
		}
		start := time.Now()
		ents, err := ioutil.ReadDir(dir)
		if err != nil {
			ensure(dir).Skipped = true
			continue
		}
		fsDir := ensure(dir)
		for _, fi := range ents {
			if fi.IsDir() {
				q.PushBack(filepath.Join(dir, fi.Name()))
				continue
			}
			fsDir.Size += fi.Size()
			fsDir.FileTypes[classifyExtension(fi.Name())] += fi.Size()
			fsDir.FileCount++
			mt := fi.ModTime()
			if fsDir.Oldest.IsZero() || mt.Before(fsDir.Oldest) {
				fsDir.Oldest = mt
			}
			if mt.After(fsDir.Newest) {
				fsDir.Newest = mt
			}
			if time.Since(start) > slow {
				fsDir.Skipped = true
				break
			}
		}
		fsDir.Total = fsDir.Size
		atomic.AddInt64(&dirCnt, 1)
		atomic.AddInt64(&bytesTotal, fsDir.Size)
		prog <- progressUpdate{dir, atomic.LoadInt64(&dirCnt), atomic.LoadInt64(&bytesTotal)}
	}
	return res
}

func aggregateTotals(m map[string]*FolderSize) {
	paths := make([]string, 0, len(m))
	for p := range m {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool {
		return strings.Count(paths[i], string(os.PathSeparator)) > strings.Count(paths[j], string(os.PathSeparator))
	})
	for _, p := range paths {
		fs := m[p]
		par := filepath.Dir(p)
		if par == p {
			continue
		}
		ps := m[par]
		if ps == nil {
			ps = &FolderSize{Path: par, FileTypes: map[string]int64{}}
			m[par] = ps
		}
		ps.Total += fs.Total
		ps.FileCount += fs.FileCount
		if ps.Oldest.IsZero() || (!fs.Oldest.IsZero() && fs.Oldest.Before(ps.Oldest)) {
			ps.Oldest = fs.Oldest
		}
		if fs.Newest.After(ps.Newest) {
			ps.Newest = fs.Newest
		}
		for c, s := range fs.FileTypes {
			ps.FileTypes[c] += s
		}
	}
}

func directChildren(m map[string]*FolderSize, par string) []*FolderSize {
	var out []*FolderSize
	for p, fs := range m {
		if filepath.Dir(p) == par && p != par {
			out = append(out, fs)
		}
	}
	return out
}

func progressReporter(ctx context.Context, prog <-chan progressUpdate, done chan<- struct{}) {
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	var last progressUpdate
	for {
		select {
		case <-ctx.Done():
			fmt.Printf("\r\033[K")
			done <- struct{}{}
			return
		case u, ok := <-prog:
			if !ok {
				fmt.Printf("\r\033[K")
				done <- struct{}{}
				return
			}
			last = u
		case <-tick.C:
			fmt.Printf("\r\033[K%sScanning:%s %s%-40s%s | %sDirs:%s %d | %sSize:%s %s",
				ColorCyan, ColorReset, Bold, shortenPath(last.CurrentDir, 40), ColorReset,
				ColorYellow, ColorReset, last.NumDirs,
				ColorGreen, ColorReset, formatSize(last.BytesTotal))
		}
	}
}

func printFat(fs *FolderSize, all map[string]*FolderSize, prev map[string]int64) {
	fmt.Printf("\n%s%s%s  %s  (%d files)\n", Bold, fs.Path, ColorReset, formatSize(fs.Total), fs.FileCount)
	if !fs.Oldest.IsZero() {
		fmt.Printf("   date span: %s – %s\n", fs.Oldest.Format("2006-01-02"), fs.Newest.Format("2006-01-02"))
	}
	avg := int64(0)
	if fs.FileCount > 0 {
		avg = fs.Total / fs.FileCount
	}
	if avg < 64<<10 && fs.FileCount > 1000 {
		fmt.Printf("   ⚠ many tiny files (avg %.0f KB)\n", float64(avg)/(1<<10))
	}
	fmt.Printf("   mix: %s\n", formatFileTypeRatios(fs.FileTypes, fs.Total))
	kids := directChildren(all, fs.Path)
	if len(kids) > 0 {
		sort.Slice(kids, func(i, j int) bool { return kids[i].Total > kids[j].Total })
		dom := float64(kids[0].Total) / float64(fs.Total)
		if dom > 0.8 {
			fmt.Printf("   ↳ dominant: %s (%s, %.1f%%)\n", filepath.Base(kids[0].Path), formatSize(kids[0].Total), dom*100)
		} else {
			fmt.Println("   top sub-folders:")
			for i, k := range kids {
				if i >= 5 || float64(k.Total)/float64(fs.Total) < 0.05 {
					break
				}
				fmt.Printf("      • %-30s %6.1f%%  %s\n", filepath.Base(k.Path), float64(k.Total)*100/float64(fs.Total), formatSize(k.Total))
			}
		}
	}
	if old, ok := prev[fs.Path]; ok && old != fs.Total {
		diff := fs.Total - old
		sign := "+"
		if diff < 0 {
			sign = ""
		}
		fmt.Printf("   growth: %s%s (%s)\n", sign, formatSize(diff), formatSize(old))
	}
}

func main() {
	help := flag.Bool("help", false, "")
	vers := flag.Bool("version", false, "")
	topN := flag.Int("top", 15, "")
	slow := flag.Duration("slow-threshold", 2*time.Second, "")
	minSizeStr := flag.String("min-size", "100G", "")
	var exclude multiFlag
	flag.Var(&exclude, "exclude", "")
	flag.Parse()
	if *help {
		flag.Usage()
		return
	}
	if *vers {
		fmt.Println("find-large-dirs", version)
		return
	}
	root := "/"
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}
	minBytes, err := parseSize(*minSizeStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	prevMap, prevTime := loadPrev(dbPath())
	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig
		fmt.Fprintln(os.Stderr, "\nInterrupted – finalising…")
		cancel()
	}()
	prog := make(chan progressUpdate, 16)
	done := make(chan struct{})
	go progressReporter(ctx, prog, done)
	fmt.Printf("Scanning '%s'…\n\n", root)
	m := bfsScan(ctx, root, exclude, *slow, prog)
	close(prog)
	<-done
	fmt.Println()
	aggregateTotals(m)
	var fat []*FolderSize
	for _, fs := range m {
		if fs.Path == root {
			continue
		}
		if fs.Total >= minBytes {
			fat = append(fat, fs)
		}
	}
	sort.Slice(fat, func(i, j int) bool { return fat[i].Total > fat[j].Total })
	if len(fat) == 0 {
		for _, fs := range m {
			if fs.Path == root {
				continue
			}
			fat = append(fat, fs)
		}
		sort.Slice(fat, func(i, j int) bool { return fat[i].Total > fat[j].Total })
		if len(fat) > *topN {
			fat = fat[:*topN]
		}
		fmt.Printf("Top %d directories (no one reached %s):\n", len(fat), formatSize(minBytes))
	} else if len(fat) > *topN {
		fat = fat[:*topN]
	}
	for _, fs := range fat {
		printFat(fs, m, prevMap)
	}
	if !prevTime.IsZero() {
		fmt.Printf("\nTime since previous scan: %s\n", time.Since(prevTime).Round(time.Second))
	}
	saveCurrent(dbPath(), m)
}

