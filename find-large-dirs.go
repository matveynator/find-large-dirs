package main

import (
    "bufio"
    "container/list"
    "context"
    "flag"
    "fmt"
    "io/ioutil"
    "os"
    "os/exec"
    "os/signal"
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
   GLOBAL
*/

var version = "dev"

// FolderSize — данные о размере конкретной папки.
type FolderSize struct {
    Path      string
    Size      int64
    Skipped   bool
    Duplicate bool // true, если (dev, inode) уже встречались
}

// NetworkMount — описание сетевого монтирования.
type NetworkMount struct {
    MountPoint  string
    FsType      string
    DisplayName string
}

/*
    UTILITY FUNCTIONS
*/

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

func shortenPath(path string, maxLen int) string {
    if len(path) <= maxLen {
        return path
    }
    return path[:maxLen-3] + "..."
}

// isExcluded — пропустить ли системные "особые" каталоги (proc, sys, dev...).
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
*/

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
*/

func detectNetworkFileSystems(rootPath string) (map[string]string, []NetworkMount, error) {
    if !isRootPath(rootPath) {
        return nil, nil, nil
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
        return nil, nil, nil
    }
}

func isRootPath(path string) bool {
    if runtime.GOOS == "windows" {
        if len(path) == 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
            return true
        }
        return false
    }
    return path == "/"
}

func detectNetworkFSLinux() (map[string]string, []NetworkMount, error) {
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
        mountPoint := fields[len(fields)-1]
        fsSpec := fields[0]
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
        parts := strings.Split(line, " ")
        if len(parts) < 4 {
            continue
        }
        mountPoint := parts[2]
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

func detectNetworkFSWindows() (map[string]string, []NetworkMount, error) {
    cmd := exec.Command("net", "use")
    out, err := cmd.Output()
    if err != nil {
        return nil, nil, err
    }
    netMap := make(map[string]string)
    var nets []NetworkMount
    lines := strings.Split(string(out), "\n")
    for _, line := range lines {
        if strings.Contains(line, `\\`) {
            fields := strings.Fields(line)
            for _, f := range fields {
                if strings.HasPrefix(f, `\\`) {
                    netMap[f] = "windows-network"
                    nets = append(nets, NetworkMount{
                        MountPoint:  f,
                        FsType:      "windows-network",
                        DisplayName: fmt.Sprintf("Network FS (%s)", f),
                    })
                }
            }
        }
    }
    return netMap, nets, nil
}

func isNetworkLike(fsType string) bool {
    fsType = strings.ToLower(fsType)
    return strings.Contains(fsType, "nfs") ||
        strings.Contains(fsType, "cifs") ||
        strings.Contains(fsType, "smb") ||
        strings.Contains(fsType, "sshfs") ||
        strings.Contains(fsType, "ftp") ||
        strings.Contains(fsType, "http") ||
        strings.Contains(fsType, "dav")
}

/*
    (DEV, INODE) TRACKING
*/

type devIno struct {
    Dev uint64
    Ino uint64
}

// getDevInode — для Unix возвращаем (dev, inode), для Windows в данном примере — false.
func getDevInode(path string) (devIno, bool) {
    if runtime.GOOS == "windows" {
        return devIno{}, false
    }
    fi, err := os.Lstat(path)
    if err != nil {
        return devIno{}, false
    }
    statT, ok := fi.Sys().(*syscall.Stat_t)
    if !ok {
        return devIno{}, false
    }
    return devIno{Dev: uint64(statT.Dev), Ino: statT.Ino}, true
}

/*
    BFS + CONTEXT
    Мы добавляем проверку: select { case <-ctx.Done(): ... } в цикле.
    Если контекст отменён (при получении сигнала), мы останавливаем сканирование
    и возвращаем то, что успели посчитать.
*/

// scanProgress — структура для передачи инфы в горутину прогресса.
type scanProgress struct {
    CurrentDir   string
    DirsCount    int64
    BytesSoFar   int64
    DuplicateHit bool
}

func bfsComputeSizes(
    ctx context.Context,
    root string,
    netMap map[string]string,
    slowThreshold time.Duration,
    progressChan chan<- scanProgress,
) []FolderSize {

    resultsMap := make(map[string]*FolderSize)
    var dirCount int64
    var totalBytes int64
    visitedDevIno := make(map[devIno]bool)

    queue := list.New()
    queue.PushBack(root)
    resultsMap[root] = &FolderSize{Path: root}

BFSLOOP:
    for queue.Len() > 0 {
        // Сначала проверяем, не отменён ли контекст
        select {
        case <-ctx.Done():
            // Контекст отменён: прерываем BFS
            break BFSLOOP
        default:
        }

        e := queue.Front()
        queue.Remove(e)
        dirPath := e.Value.(string)

        if _, ok := netMap[dirPath]; ok {
            fsEntry := ensureFolderSize(resultsMap, dirPath)
            fsEntry.Skipped = true
            continue
        }
        if isExcluded(dirPath) {
            fsEntry := ensureFolderSize(resultsMap, dirPath)
            fsEntry.Skipped = true
            continue
        }

        // Проверяем dev+inode
        devInoVal, canCheck := getDevInode(dirPath)
        if canCheck {
            if visitedDevIno[devInoVal] {
                // уже сканировали
                fsEntry := ensureFolderSize(resultsMap, dirPath)
                fsEntry.Duplicate = true
                atomic.AddInt64(&dirCount, 1)
                progressChan <- scanProgress{
                    CurrentDir:   dirPath + " (duplicate)",
                    DirsCount:    atomic.LoadInt64(&dirCount),
                    BytesSoFar:   atomic.LoadInt64(&totalBytes),
                    DuplicateHit: true,
                }
                continue
            }
            visitedDevIno[devInoVal] = true
        }

        startTime := time.Now()
        var localSize int64
        skipDir := false
        entries, err := ioutil.ReadDir(dirPath)
        if err != nil {
            skipDir = true
        } else {
            for _, fi := range entries {
                if time.Since(startTime) > slowThreshold {
                    skipDir = true
                    break
                }
                if !fi.IsDir() {
                    localSize += fi.Size()
                }
            }
        }

        fsEntry := ensureFolderSize(resultsMap, dirPath)
        fsEntry.Size = localSize
        fsEntry.Skipped = skipDir

        atomic.AddInt64(&dirCount, 1)
        if !skipDir {
            atomic.AddInt64(&totalBytes, localSize)
        }

        // Сообщаем о прогрессе
        progressChan <- scanProgress{
            CurrentDir:   dirPath,
            DirsCount:    atomic.LoadInt64(&dirCount),
            BytesSoFar:   atomic.LoadInt64(&totalBytes),
            DuplicateHit: false,
        }

        // Если директория не пропущена, добавляем сабдиры
        if !skipDir && err == nil {
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
    }

    // Превращаем в слайс, сортируем
    resultSlice := make([]FolderSize, 0, len(resultsMap))
    for _, fs := range resultsMap {
        resultSlice = append(resultSlice, *fs)
    }
    sort.Slice(resultSlice, func(i, j int) bool {
        return resultSlice[i].Size > resultSlice[j].Size
    })
    return resultSlice
}

func ensureFolderSize(m map[string]*FolderSize, path string) *FolderSize {
    fsEntry, ok := m[path]
    if !ok {
        fsEntry = &FolderSize{Path: path}
        m[path] = fsEntry
    }
    return fsEntry
}

/*
    ПРОГРЕСС ГОРУТИНА
*/

func progressReporter(
    ctx context.Context,
    progressChan <-chan scanProgress,
    doneChan chan<- bool,
    totalDiskBytes int64,
) {
    ticker := time.NewTicker(350 * time.Millisecond)
    defer ticker.Stop()

    var lastMsg scanProgress

    for {
        select {
        case <-ctx.Done():
            // Если контекст отменён (сигнал), выходим
            fmt.Printf("\r\033[K")
            doneChan <- true
            return

        case msg, ok := <-progressChan:
            if !ok {
                // Канал закрыт — сканирование окончено
                fmt.Printf("\r\033[K")
                doneChan <- true
                return
            }
            lastMsg = msg

        case <-ticker.C:
            // Периодически перерисовываем строку
            fmt.Printf("\r\033[K")
            shortDir := shortenPath(lastMsg.CurrentDir, 50)
            msgStr := fmt.Sprintf(
                " Scanned dirs: %d | Accumulated size: %s",
                lastMsg.DirsCount,
                formatSize(lastMsg.BytesSoFar),
            )
            if totalDiskBytes > 0 {
                pct := float64(lastMsg.BytesSoFar) / float64(totalDiskBytes) * 100
                if pct > 100 {
                    msgStr += fmt.Sprintf(" (%.2f%%; duplicates exist)", pct)
                } else {
                    msgStr += fmt.Sprintf(" (%.2f%% of disk)", pct)
                }
            }
            fmt.Printf("%s | scanning: %s", msgStr, shortDir)
        }
    }
}

/*
    MAIN
*/

func main() {
    // Разбираем флаги
    flag.Usage = func() {
        fmt.Println("Usage: find-large-dirs [directory] [--top <N>] [--slow-threshold <duration>]")
        fmt.Println("[--help] [--version]")
        fmt.Println("Single-pass BFS (one-thread I/O), skipping duplicates, shows immediate progress,")
        fmt.Println("and prints partial results on interruption.")
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

    // Создаём контекст, который отменим при сигнале
    ctx, cancel := context.WithCancel(context.Background())

    // Ловим сигналы прерывания
    sigChan := make(chan os.Signal, 1)
    // Перехватываем Ctrl+C, SIGTERM и т.п.
    signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

    // Горутину, которая ждёт сигнал
    go func() {
        <-sigChan
        fmt.Fprintf(os.Stderr, "\nReceived interruption signal, finalizing...\n")
        // Отменяем контекст
        cancel()
    }()

    // Детектируем сетевые ФС (только если корень)
    netMap, netMounts, err := detectNetworkFileSystems(rootPath)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Warning: could not detect network FS: %v\n", err)
    }

    // Если корень, считаем общий размер диска
    var totalDiskBytes int64
    if isRootPath(rootPath) {
        total, _, _, err := getDiskUsageInfo(rootPath)
        if err == nil {
            totalDiskBytes = int64(total)
        }
    }

    fmt.Printf("Scanning '%s'...\n\n", rootPath)

    // Канал и горутина для прогресса
    progressChan := make(chan scanProgress, 10)
    doneChan := make(chan bool)
    go progressReporter(ctx, progressChan, doneChan, totalDiskBytes)

    // Запускаем BFS в текущей горутине
    resultFolders := bfsComputeSizes(ctx, rootPath, netMap, *slowFlag, progressChan)

    // Закрываем канал, ждём завершения прогресс-горутины
    close(progressChan)
    <-doneChan
    fmt.Println()

    // Выводим инфо о сетевых ФС
    if len(netMounts) > 0 {
        fmt.Println("Network filesystems detected (skipped entirely):")
        for _, nm := range netMounts {
            fmt.Printf("  %s => %s\n", nm.DisplayName, nm.MountPoint)
        }
        fmt.Println()
    }

    // Выводим топ N
    fmt.Printf("Top %d largest directories in '%s':\n", *topFlag, rootPath)
    w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
    count := 0
    for _, fs := range resultFolders {
        if count >= *topFlag {
            break
        }
        dp := fs.Path
        note := ""
        if fs.Skipped {
            note = "(skipped)"
        } else if fs.Duplicate {
            note = "(duplicate)"
        }
        if note != "" {
            dp += " " + note
        }
        fmt.Fprintf(w, "%-10s\t%-60s\n", formatSize(fs.Size), dp)
        count++
    }
    w.Flush()

    // Если программа прервана, пользователь всё равно увидит частичные результаты,
    // так как BFS вышел по ctx.Done(), а мы всё распечатали.
}

