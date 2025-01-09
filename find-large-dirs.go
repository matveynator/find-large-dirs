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
    Here we store the logical data we need for folder sizes and potential network mounts.
*/

type FolderSize struct {
    Path    string
    Size    int64
    Skipped bool
}

type NetworkMount struct {
    MountPoint  string
    FsType      string
    DisplayName string
}

/*
    UTILITY FUNCTIONS
*/

// formatSize converts int64 bytes to a human-readable string (KB, MB, or GB).
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

// formatSizeUint64 does the same for uint64.
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

// shortenPath truncates a path to avoid overly long lines in progress output.
func shortenPath(path string, maxLen int) string {
    if len(path) <= maxLen {
        return path
    }
    return path[:maxLen-3] + "..."
}

// isExcluded checks if a directory should be skipped entirely (e.g. /proc, /sys, etc.).
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
    Returns total, free, and used bytes for the filesystem containing 'path'.
    For simplicity, we show a Unix version with Statfs; for Windows, you'd
    likely use a different system call or library function.
*/

func getDiskUsageInfo(path string) (total, free, used uint64, err error) {
    // This works on many Unix-likes; on Windows you'd need a different approach.
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
    Мы детектируем сетевые FS только при сканировании корня (чтобы не тратить время).
    Для Linux — /proc/mounts,
    для macOS — df -h,
    для BSD — mount -l,
    для Windows — net use.
    Если это подпапка, то пропускаем детектирование.
*/

// detectNetworkFileSystems detects "network-like" filesystems if we are scanning root.
func detectNetworkFileSystems(rootPath string) (map[string]string, []NetworkMount, error) {
    // Если путь не является "корневым" для данной ОС, не делаем ничего.
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

// isRootPath упрощённо проверяет, корневая ли это директория для текущей ОС.
func isRootPath(path string) bool {
    if runtime.GOOS == "windows" {
        // Допустим, что "C:\" или "D:\" и т.д. является корнем.
        // На практике надо аккуратно проверять и формат пути.
        if len(path) == 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
            return true
        }
        return false
    }
    // На Unix-подобных считаем корень — "/"
    return path == "/"
}

// detectNetworkFSLinux читает /proc/mounts и ищет сетевые fs-типы (nfs, cifs, smb...).
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

// detectNetworkFSDarwin вызывает df -h и ищет строчки с удалённым источником (наивно).
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
        mountPoint := fields[len(fields)-1] // последнее поле
        fsSpec := fields[0]                // первое поле — источник
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

// detectNetworkFSBSD вызывает mount -l и парсит вывод, выцепляя nfs/cifs/и т.д.
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

// detectNetworkFSWindows вызывает net use и ищет строки с `\\server\share`.
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

// isNetworkLike — проверка строки fsType на типичные сетевые фс.
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
    BFS SCANNING FOR SIZES
    Мы делаем один проход, без предварительного подсчёта общего кол-ва директорий —
    чтобы сразу выводить прогресс и не ждать. По мере обхода:
      1) Считаем суммарный размер файлов в каждой папке (с проверкой slowThreshold).
      2) Сразу передаём сведения о текущей папке (и сколько всего директорий обработано,
         и какую сумму байт уже нашли) в канал для прогресс-блокировки.
      3) Если чтение папки слишком долго, помечаем её как "skipped".
*/

// bfsComputeSizes обходит дерево каталогов из root, формируя список FolderSize.
func bfsComputeSizes(
    root string,
    netMap map[string]string,
    slowThreshold time.Duration,
    progressChan chan<- scanProgress, // канал для отчёта о прогрессе
) []FolderSize {

    // Карта результатов, ключ — путь папки
    resultsMap := make(map[string]*FolderSize)

    // Счётчик — сколько директорий мы прошли
    var dirCount int64
    // Счётчик всех «найденных» байт
    var totalBytes int64

    // Очередь для обхода в ширину
    queue := list.New()
    queue.PushBack(root)

    // Заводим запись в resultsMap для корня
    resultsMap[root] = &FolderSize{Path: root, Size: 0, Skipped: false}

    for queue.Len() > 0 {
        e := queue.Front()
        queue.Remove(e)
        dirPath := e.Value.(string)

        // Если это сетевой или исключённый путь — пропустим
        if _, ok := netMap[dirPath]; ok {
            resultsMap[dirPath] = &FolderSize{Path: dirPath, Skipped: true}
            continue
        }
        if isExcluded(dirPath) {
            resultsMap[dirPath] = &FolderSize{Path: dirPath, Skipped: true}
            continue
        }

        startTime := time.Now()
        var localSize int64
        skipDir := false

        // Читаем содержимое
        entries, err := ioutil.ReadDir(dirPath)
        if err != nil {
            skipDir = true
        } else {
            // Суммируем файлы
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

        // Обновим FolderSize
        fsEntry, found := resultsMap[dirPath]
        if !found {
            fsEntry = &FolderSize{Path: dirPath}
            resultsMap[dirPath] = fsEntry
        }
        fsEntry.Size = localSize
        fsEntry.Skipped = skipDir

        // Увеличим общую сумму байт и количество директорий
        atomic.AddInt64(&dirCount, 1)
        if !skipDir {
            atomic.AddInt64(&totalBytes, localSize)
        }

        // Сообщим в канал прогресса: сколько директорий, сколько байт и что сканируем
        progressChan <- scanProgress{
            CurrentDir:  dirPath,
            DirsCount:   atomic.LoadInt64(&dirCount),
            BytesSoFar:  atomic.LoadInt64(&totalBytes),
        }

        // Если директория не пропущена — добавляем сабдиректории в очередь
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

    // Конвертация из map в слайс
    resultSlice := make([]FolderSize, 0, len(resultsMap))
    for _, fs := range resultsMap {
        resultSlice = append(resultSlice, *fs)
    }
    // Сортируем по убыванию размера
    sort.Slice(resultSlice, func(i, j int) bool {
        return resultSlice[i].Size > resultSlice[j].Size
    })
    return resultSlice
}

/*
    ПРОГРЕСС
    Мы организуем отдельную горутину, которая слушает наш progressChan и выводит данные:
      - Текущая директория
      - Кол-во уже обработанных директорий
      - Суммарный размер найденных файлов
      - Процент относительно всего диска (если rootPath — действительно корень).
    Завершение: как только канал закрыт, выходим из прогресс-цикла.
*/

// scanProgress — структура для передачи в прогресс-горутину
type scanProgress struct {
    CurrentDir string
    DirsCount  int64
    BytesSoFar int64
}

// progressReporter запускается в отдельной горутине: читает из progressChan
// и выводит прогресс до тех пор, пока канал не закроется.
func progressReporter(
    progressChan <-chan scanProgress,
    doneChan chan<- bool,
    totalDiskBytes int64, // 0 если не считаем процент
) {
    ticker := time.NewTicker(300 * time.Millisecond)
    defer ticker.Stop()

    // Храним последнее полученное сообщение о прогрессе
    var lastMsg scanProgress

    for {
        select {
        case msg, ok := <-progressChan:
            if !ok {
                // Канал закрыт — значит, сканирование окончено
                fmt.Printf("\r\033[K") // очистить строку
                doneChan <- true
                return
            }
            // Обновим локальные данные
            lastMsg = msg

        case <-ticker.C:
            // Раз в 300 мс перерисовываем строку прогресса
            fmt.Printf("\r\033[K") // очистить строку

            // Сформируем сообщение
            shortDir := shortenPath(lastMsg.CurrentDir, 50)
            msgStr := fmt.Sprintf(
                " Scanned dirs: %d | Accumulated size: %s",
                lastMsg.DirsCount,
                formatSize(lastMsg.BytesSoFar),
            )

            // Если известен общий объём диска, покажем процент
            if totalDiskBytes > 0 {
                percent := float64(lastMsg.BytesSoFar) / float64(totalDiskBytes) * 100
                if percent < 0 {
                    percent = 0
                }
                if percent > 100 {
                    percent = 100
                }
                msgStr += fmt.Sprintf(" (%.2f%% of disk)", percent)
            }

            fmt.Printf("%s | scanning: %s", msgStr, shortDir)
        }
    }
}

/*
    MAIN
    1) Парсим флаги
    2) Детектируем сетевые FS (только если корень)
    3) Если корень, получаем общий размер диска (для процента)
    4) Стартуем горутину прогресса
    5) Запускаем BFS в основном потоке (одна горутина для IO)
    6) По окончании закрываем канал прогресса, ждём doneChan
    7) Выводим топ N директорий
*/

func main() {
    // Флаги командной строки
    flag.Usage = func() {
        fmt.Println("Usage: find-large-dirs [directory] [--top <N>] [--slow-threshold <duration>]")
        fmt.Println("[--help] [--version]")
        fmt.Println("Single-pass BFS, skipping network mounts and system dirs, showing immediate progress.")
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

    // Определяем корень для сканирования
    rootPath := "/"
    if flag.NArg() > 0 {
        rootPath = flag.Arg(0)
    }

    // 2) Детектируем сетевые FS, если это действительно корень
    netMap, netMounts, err := detectNetworkFileSystems(rootPath)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Warning: could not detect network FS: %v\n", err)
    }

    // 3) Если это корень, попробуем узнать общий размер диска — чтобы показывать проценты.
    var totalDiskBytes int64
    if isRootPath(rootPath) {
        total, _, _, err := getDiskUsageInfo(rootPath)
        if err == nil {
            totalDiskBytes = int64(total)
        }
    }

    // Для эстетики выведем простую инфо о корне/папке (быстрая прикидка)
    fmt.Printf("Scanning '%s'...\n\n", rootPath)

    // 4) Запускаем горутину прогресса
    progressChan := make(chan scanProgress, 1)
    doneChan := make(chan bool)
    go progressReporter(progressChan, doneChan, totalDiskBytes)

    // 5) Делаем один проход BFS — чтобы не перегружать диск,
    //    вся работа с каталогами идёт в одном потоке (эта горутина).
    resultFolders := bfsComputeSizes(rootPath, netMap, *slowFlag, progressChan)

    // 6) Закрываем канал прогресса и ждём его завершения.
    close(progressChan)
    <-doneChan
    fmt.Println()

    // Если сетевые ФС есть — выведем их
    if len(netMounts) > 0 {
        fmt.Println("Network filesystems detected (skipped entirely):")
        for _, nm := range netMounts {
            fmt.Printf("  %s => %s\n", nm.DisplayName, nm.MountPoint)
        }
        fmt.Println()
    }

    // 7) Печатаем топ N директорий
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
        fmt.Fprintf(w, "%-10s\t%-60s\n", formatSize(fs.Size), dp)
        count++
    }
    w.Flush()
}

