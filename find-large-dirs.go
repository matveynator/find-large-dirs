package main

import (
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
*/

var version = "dev" // Program version set at build time

/*
    DATA STRUCTURES
*/

type FolderSize struct {
    Path      string
    Size      int64
    Skipped   bool
    Duplicate bool // признак, что это дубликат (dev+inode уже встречались)
}

type NetworkMount struct {
    MountPoint  string
    FsType      string
    DisplayName string
}

/*
    UTILITY FUNCTIONS
*/

// formatSize - перевод int64 байт в удобочитаемый формат (KB, MB, GB).
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

// formatSizeUint64 аналогичен для uint64.
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

// shortenPath - обрезает путь для компактного отображения в прогрессе.
func shortenPath(path string, maxLen int) string {
    if len(path) <= maxLen {
        return path
    }
    return path[:maxLen-3] + "..."
}

// isExcluded - проверяем, не надо ли пропустить системные каталоги (например /proc, /sys...).
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
    Для Unix-подобных используем Statfs, чтобы получить total/free/used.
    Для Windows в реальной практике надо звать WinAPI (GetDiskFreeSpaceEx), но
    здесь мы для упрощения не показываем полноценный пример.
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
    (Делаем только для «корневых» путей)
*/

// detectNetworkFileSystems определяет «сетевые» файловые системы (nfs, cifs…) по ОС.
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

// isRootPath - упрощённая проверка, является ли путь корневым для данной ОС.
func isRootPath(path string) bool {
    if runtime.GOOS == "windows" {
        // Считаем, что "C:\" и т.п. — корневой
        if len(path) == 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
            return true
        }
        return false
    }
    // На Unix-подобных считаем / корнем
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

// isNetworkLike - проверка fsType на принадлежность к сетевым (nfs, cifs, smb, sshfs...).
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
    На Unix мы можем использовать fi.Sys().(*syscall.Stat_t) и брать Dev, Ino,
    чтобы понять, не сканировали ли мы этот каталог уже (т. е. физически тот же).
    Для Windows (где нет стандартных inode), здесь для простоты пропустим.
*/

type devIno struct {
    Dev uint64
    Ino uint64
}

// getDevInode пытается получить (device, inode) для path.
// Если невозможно (на Windows без доп. вызовов), возвращаем нули и false.
func getDevInode(path string) (devIno, bool) {
    if runtime.GOOS == "windows" {
        // Упрощённо — на Windows не реализуем (dev, inode).
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
    BFS SCANNING
    - Один проход в одном потоке.
    - По мере чтения каталогов суммируем размеры файлов.
    - Если папка (dev, inode) уже встречалась — помечаем её Duplicate и пропускаем повторный учёт.
    - Сообщаем в канал прогресса, чтобы UI сразу обновлялся.
*/

func bfsComputeSizes(
    root string,
    netMap map[string]string,
    slowThreshold time.Duration,
    progressChan chan<- scanProgress,
) []FolderSize {

    resultsMap := make(map[string]*FolderSize)

    var dirCount int64   // Сколько директорий мы уже обработали
    var totalBytes int64 // Суммарный объём учтённых файлов

    // visitedDevIno хранит все (dev,ino), которые мы уже учитывали
    visitedDevIno := make(map[devIno]bool)

    queue := list.New()
    queue.PushBack(root)
    resultsMap[root] = &FolderSize{Path: root, Size: 0}

    for queue.Len() > 0 {
        e := queue.Front()
        queue.Remove(e)
        dirPath := e.Value.(string)

        // Проверка на сетевой монт и excluded
        if _, ok := netMap[dirPath]; ok {
            r := ensureFolderSize(resultsMap, dirPath)
            r.Skipped = true
            continue
        }
        if isExcluded(dirPath) {
            r := ensureFolderSize(resultsMap, dirPath)
            r.Skipped = true
            continue
        }

        // Проверим (dev, inode)
        devInoVal, canCheckDevIno := getDevInode(dirPath)
        if canCheckDevIno {
            if visitedDevIno[devInoVal] {
                // Уже сканировали
                r := ensureFolderSize(resultsMap, dirPath)
                r.Duplicate = true
                // Сообщаем в прогресс, что встретили дубль
                atomic.AddInt64(&dirCount, 1)
                progressChan <- scanProgress{
                    CurrentDir:   dirPath + " (duplicate)",
                    DirsCount:    atomic.LoadInt64(&dirCount),
                    BytesSoFar:   atomic.LoadInt64(&totalBytes),
                    DuplicateHit: true,
                }
                continue
            } else {
                // Помечаем, что теперь сканируем
                visitedDevIno[devInoVal] = true
            }
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

        // Обновляем глобальные счётчики
        atomic.AddInt64(&dirCount, 1)
        if !skipDir {
            atomic.AddInt64(&totalBytes, localSize)
        }

        // Сообщаем в прогресс
        progressChan <- scanProgress{
            CurrentDir:   dirPath,
            DirsCount:    atomic.LoadInt64(&dirCount),
            BytesSoFar:   atomic.LoadInt64(&totalBytes),
            DuplicateHit: false,
        }

        if !skipDir && err == nil {
            // Добавляем сабдиры в очередь
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

    // Превращаем карту в слайс
    folderSlice := make([]FolderSize, 0, len(resultsMap))
    for _, val := range resultsMap {
        folderSlice = append(folderSlice, *val)
    }
    // Сортируем по убыванию
    sort.Slice(folderSlice, func(i, j int) bool {
        return folderSlice[i].Size > folderSlice[j].Size
    })
    return folderSlice
}

// ensureFolderSize - вспомогательная функция, чтоб не писать один и тот же код.
func ensureFolderSize(m map[string]*FolderSize, path string) *FolderSize {
    fsEntry, ok := m[path]
    if !ok {
        fsEntry = &FolderSize{Path: path}
        m[path] = fsEntry
    }
    return fsEntry
}

/*
    PROGRESS
    - Мы хотим, чтобы в реальном времени выводилось число директорий, общий размер
      и процент (если известно, что сканируем корень и можем вычислить totalDiskBytes).
    - Если видим, что пришёл DuplicateHit, можем дополнительно указать "(duplicate)" в выводе.
*/

type scanProgress struct {
    CurrentDir   string
    DirsCount    int64
    BytesSoFar   int64
    DuplicateHit bool
}

func progressReporter(
    progressChan <-chan scanProgress,
    doneChan chan<- bool,
    totalDiskBytes int64, // 0, если не считаем процент
) {
    ticker := time.NewTicker(350 * time.Millisecond)
    defer ticker.Stop()

    var lastMsg scanProgress

    for {
        select {
        case msg, ok := <-progressChan:
            if !ok {
                // Канал закрыт, значит всё закончено
                fmt.Printf("\r\033[K")
                doneChan <- true
                return
            }
            lastMsg = msg

        case <-ticker.C:
            fmt.Printf("\r\033[K") // очистить строку

            shortDir := shortenPath(lastMsg.CurrentDir, 50)
            msgStr := fmt.Sprintf(
                " Scanned dirs: %d | Accumulated size: %s",
                lastMsg.DirsCount,
                formatSize(lastMsg.BytesSoFar),
            )

            // Если есть info о полном объёме диска, выводим %
            if totalDiskBytes > 0 {
                pct := float64(lastMsg.BytesSoFar) / float64(totalDiskBytes) * 100
                if pct > 100 {
                    // Если «выскочили» за 100%, укажем, что часть — дубликаты
                    msgStr += fmt.Sprintf(" (%.2f%%; duplicates exist)", pct)
                } else {
                    msgStr += fmt.Sprintf(" (%.2f%% of disk)", pct)
                }
            }

            // Отмечаем, если текущий dir — дубль
            // (Хотя мы уже добавили "(duplicate)" в CurrentDir, можно повторить)
            fmt.Printf("%s | scanning: %s", msgStr, shortDir)
        }
    }
}

/*
    MAIN
*/

func main() {
    flag.Usage = func() {
        fmt.Println("Usage: find-large-dirs [directory] [--top <N>] [--slow-threshold <duration>]")
        fmt.Println("[--help] [--version]")
        fmt.Println("Single-pass BFS (one-thread I/O), skipping duplicates, shows immediate progress.")
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

    // Детектируем сетевые FS (только если корень)
    netMap, netMounts, err := detectNetworkFileSystems(rootPath)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Warning: could not detect network FS: %v\n", err)
    }

    // Считаем общий размер диска, если корень
    var totalDiskBytes int64
    if isRootPath(rootPath) {
        total, _, _, err := getDiskUsageInfo(rootPath)
        if err == nil {
            totalDiskBytes = int64(total)
        }
    }

    fmt.Printf("Scanning '%s'...\n\n", rootPath)

    // Запускаем горутину прогресса
    progressChan := make(chan scanProgress, 10)
    doneChan := make(chan bool)
    go progressReporter(progressChan, doneChan, totalDiskBytes)

    // Выполняем BFS в текущей (main) горутине
    resultFolders := bfsComputeSizes(rootPath, netMap, *slowFlag, progressChan)

    // Закрываем канал прогресса и ждём завершения
    close(progressChan)
    <-doneChan
    fmt.Println()

    // Выводим сетевые ФС
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
}

