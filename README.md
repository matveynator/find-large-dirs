# **`find-large-dirs` – покажи, где живёт жир** 🐘

Утилита показывает, какие папки занимают больше всего места, в каком виде там данные (архивы, базы, код, видео и т.п.), и как папки разрастаются со временем.

---

## 🚀 Установка (одной строкой)

Просто скопируйте нужную строку и вставьте в терминал:

### **Linux (AMD64)**

```bash
sudo curl -L https://github.com/matveynator/find-large-dirs/releases/latest/download/find-large-dirs_linux_amd64 -o /usr/local/bin/find-large-dirs; sudo chmod +x /usr/local/bin/find-large-dirs; find-large-dirs --version;
```

### **Linux (ARM64)**

```bash
sudo curl -L https://github.com/matveynator/find-large-dirs/releases/latest/download/find-large-dirs_linux_arm64 -o /usr/local/bin/find-large-dirs; sudo chmod +x /usr/local/bin/find-large-dirs; find-large-dirs --version;
```

### **macOS (Intel)**

```bash
sudo curl -L https://github.com/matveynator/find-large-dirs/releases/latest/download/find-large-dirs_darwin_amd64 -o /usr/local/bin/find-large-dirs; sudo chmod +x /usr/local/bin/find-large-dirs; find-large-dirs --version;
```

### **macOS (Apple Silicon)**

```bash
sudo curl -L https://github.com/matveynator/find-large-dirs/releases/latest/download/find-large-dirs_darwin_arm64 -o /usr/local/bin/find-large-dirs; sudo chmod +x /usr/local/bin/find-large-dirs; find-large-dirs --version;
```

### **FreeBSD (AMD64)**

```bash
sudo fetch -o /usr/local/bin/find-large-dirs https://github.com/matveynator/find-large-dirs/releases/latest/download/find-large-dirs_freebsd_amd64 && chmod +x /usr/local/bin/find-large-dirs && find-large-dirs --version;
```

### **Windows (PowerShell)**

```powershell
Invoke-WebRequest https://github.com/matveynator/find-large-dirs/releases/latest/download/find-large-dirs_windows_amd64.exe -OutFile $env:ProgramFiles\\find-large-dirs.exe
& $env:ProgramFiles\\find-large-dirs.exe --version
```

📦 Для всех остальных платформ — [см. список релизов](https://github.com/matveynator/find-large-dirs/releases/latest)

---

## 🧪 Пример запуска

```bash
$ find-large-dirs /data
Scanning '/data'…

Top 10 directories (no one reached 200.00 GB):

/data                       75.2 GB   (148 231 files)
   mix: 45 % Archive, 30 % Other, 15 % Video, 10 % Code
   top sub-folders:
      • backups              40.0 %   30.1 GB
      • media                26.5 %   19.9 GB
      • projects             18.8 %   14.1 GB

/data/backups               30.1 GB   (5 631 files)
   ↳ dominant: nightlies (28.9 GB, 96 %)

...
```

📌 За секунды видно: всё сожрали ночные бэкапы. Можно навести порядок.

---

## 🔧 Часто используемые параметры

| Параметр              | Описание                                      | Пример                                 |
| --------------------- | --------------------------------------------- | -------------------------------------- |
| `--top 25`            | Показать 25 крупнейших директорий             | `find-large-dirs --top 25 /`           |
| `--min-size 300G`     | «Жирными» считаются только папки ≥ 300 GB     | `find-large-dirs --min-size 300G /srv` |
| `--exclude /tmp`      | Исключить путь                                | `--exclude /tmp --exclude /mnt/slow`   |
| `--slow-threshold 3s` | Пометить как «slow» папки, скан которых > 3 с |                                        |
| `--json`              | Вывести результат в JSON (для автоматизации)  |                                        |
| `--version`           | Показать текущую версию                       |                                        |

---

🎯 Подходит системным администраторам, DevOps-инженерам и обычным пользователям.
⏱ Быстро, просто, без установки зависимостей.
