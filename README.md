# **`find-large-dirs` - Scan and Find Large Folders Quickly**

```plaintext
find-large-dirs /backup/
Scanning '/backup/'...

Scanning: /backup/192.168.1.100/vservers/server01... | Dirs: 37399 | Size: 16480.84 GB^C
Interrupted. Finalizing...

Top 15 largest directories in '/backup/':
4184.38 GB    /backup/infra.example.net/backup-server/backup/static.101.102.103.104.clients.example.com/sql (+220427.32 MB, +5.42%)
               -> File types: 100.00% Archive, 0.00% Document
2271.62 GB    /backup/code.example.net/backup/archives (+14255.33 MB, +0.62%)
               -> File types: 100.00% Archive, 0.00% Other
1415.92 GB    /backup/data.example.com/backup/data.example.com/sql (+42054.95 MB, +2.99%)
               -> File types: 100.00% Archive
1363.93 GB    /backup/infra.example.net/backup-server/backup/static.102.103.104.105.clients.example.com/sql (+64629.97 MB, +4.85%)
               -> File types: 100.00% Archive, 0.00% Document
1003.08 GB    /backup/infra.example.net/backup-server/backup/app.example.com (+28318.83 MB, +2.84%)
               -> File types: 99.67% Archive, 0.33% Other, 0.00% Application
585.23 GB     /backup/infra.example.net/backup-server/backup/static.203.204.205.206.clients.example.com/sql (+27490.80 MB, +4.81%)
               -> File types: 100.00% Archive, 0.00% Document
581.46 GB     /backup/infra.example.net/backup-server/backup/project.net/sql (+27469.23 MB, +4.84%)
               -> File types: 100.00% Archive, 0.00% Document
350.87 GB     /backup/service-node/reserv-web/backup/data
               -> File types: 100.00% Archive, 0.00% Other
251.10 GB     /backup/203.0.113.100/app.example.com/rootfs/backup/archives (-22919.93 MB, -8.18%)
               -> File types: 98.19% Archive, 1.81% Other, 0.00% Application
193.09 GB     /backup/service-node/reserv-db/backup/static.104.105.106.107.clients.example.com/sql
               -> File types: 100.00% Archive
125.53 GB     /backup/cloud-storage/example.app/rootfs/backup/mongo
               -> File types: 100.00% DB-Backup
116.76 GB     /backup/infra.example.net/backup-server/backup/cards.example/sql (+5368.57 MB, +4.70%)
               -> File types: 100.00% Archive
105.15 GB     /backup/partner.example.com-centos/backup/partner.example.com-centos (+356.57 MB, +0.33%)
               -> File types: 100.00% Other
101.53 GB     /backup/data.example.com/backup/archives (+3296.47 MB, +3.27%)
               -> File types: 100.00% Archive, 0.00% Other
62.92 GB      /backup/192.168.1.100/vservers/php7/opt/cache
               -> File types: 100.00% Archive

Time since last scan: 67h17m2s
```

---

## **Quick Install with `curl`**

We’ve made it easy for you. Copy, paste, and go!

### **Linux (AMD64)**

```bash
sudo curl -L https://files.zabiyaka.net/find-large-dirs/latest/no-gui/linux/amd64/find-large-dirs -o /usr/local/bin/find-large-dirs; sudo chmod +x /usr/local/bin/find-large-dirs; find-large-dirs --version;
```

### **Linux (ARM64)**

```bash
sudo curl -L https://files.zabiyaka.net/find-large-dirs/latest/no-gui/linux/arm64/find-large-dirs -o /usr/local/bin/find-large-dirs; sudo chmod +x /usr/local/bin/find-large-dirs; find-large-dirs --version;
```


### **macOS (Intel)**

```bash
sudo curl -L https://files.zabiyaka.net/find-large-dirs/latest/no-gui/mac/amd64/find-large-dirs -o /usr/local/bin/find-large-dirs; sudo chmod +x /usr/local/bin/find-large-dirs; find-large-dirs --version;
```


### **macOS (Apple Silicon)**

```bash
sudo curl -L https://files.zabiyaka.net/find-large-dirs/latest/no-gui/mac/arm64/find-large-dirs -o /usr/local/bin/find-large-dirs; sudo chmod +x /usr/local/bin/find-large-dirs; find-large-dirs --version;
```

### **FreeBSD (AMD64)**

```bash
sudo curl -L https://files.zabiyaka.net/find-large-dirs/latest/no-gui/freebsd/amd64/find-large-dirs -o /usr/local/bin/find-large-dirs; sudo chmod +x /usr/local/bin/find-large-dirs; find-large-dirs --version;
```

### **FreeBSD (ARM64)**

```bash
sudo curl -L https://files.zabiyaka.net/find-large-dirs/latest/no-gui/freebsd/arm64/find-large-dirs -o /usr/local/bin/find-large-dirs; sudo chmod +x /usr/local/bin/find-large-dirs; find-large-dirs --version;
```

### **OpenBSD (AMD64)**

```bash
sudo curl -L https://files.zabiyaka.net/find-large-dirs/latest/no-gui/openbsd/amd64/find-large-dirs -o /usr/local/bin/find-large-dirs; sudo chmod +x /usr/local/bin/find-large-dirs; find-large-dirs --version;
```

### **OpenBSD (ARM64)**

```bash
sudo curl -L https://files.zabiyaka.net/find-large-dirs/latest/no-gui/openbsd/arm64/find-large-dirs -o /usr/local/bin/find-large-dirs; sudo chmod +x /usr/local/bin/find-large-dirs; find-large-dirs --version;
```

### **Windows (AMD64)**

```cmd
certutil -urlcache -split -f https://files.zabiyaka.net/find-large-dirs/latest/no-gui/windows/amd64/find-large-dirs.exe "C:\Windows\System32\find-large-dirs.exe"
find-large-dirs --version
```

### **Windows (ARM64)**

```cmd
certutil -urlcache -split -f https://files.zabiyaka.net/find-large-dirs/latest/no-gui/windows/arm64/find-large-dirs.exe "C:\Windows\System32\find-large-dirs.exe"
find-large-dirs --version
```

### **Windows (386)**

```cmd
certutil -urlcache -split -f https://files.zabiyaka.net/find-large-dirs/latest/no-gui/windows/386/find-large-dirs.exe "C:\Windows\System32\find-large-dirs.exe"
find-large-dirs --version
```

### **Windows (ARM)**

```cmd
certutil -urlcache -split -f https://files.zabiyaka.net/find-large-dirs/latest/no-gui/windows/arm/find-large-dirs.exe "C:\Windows\System32\find-large-dirs.exe"
find-large-dirs --version
```

Other platforms and architectures? Don’t worry, we’ve got you covered. See the table below for the full list.

Here is the table with **direct download links** for all available platforms and architectures.

| **Operating System** | **Architectures and Download Links** |
|-----------------------|--------------------------------------|
| ![Linux](https://edent.github.io/SuperTinyIcons/images/svg/linux.svg) **Linux** | [amd64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/linux/amd64/find-large-dirs), [arm64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/linux/arm64/find-large-dirs), [arm](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/linux/arm/find-large-dirs), [386](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/linux/386/find-large-dirs), [ppc64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/linux/ppc64/find-large-dirs), [ppc64le](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/linux/ppc64le/find-large-dirs), [riscv64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/linux/riscv64/find-large-dirs), [loong64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/linux/loong64/find-large-dirs), [mips](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/linux/mips/find-large-dirs), [mipsle](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/linux/mipsle/find-large-dirs), [mips64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/linux/mips64/find-large-dirs), [mips64le](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/linux/mips64le/find-large-dirs), [s390x](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/linux/s390x/find-large-dirs) |
| ![Windows](https://edent.github.io/SuperTinyIcons/images/svg/windows.svg) **Windows** | [amd64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/windows/amd64/find-large-dirs.exe), [arm64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/windows/arm64/find-large-dirs.exe), [arm](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/windows/arm/find-large-dirs.exe), [386](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/windows/386/find-large-dirs.exe) |
| ![macOS](https://edent.github.io/SuperTinyIcons/images/svg/apple.svg) **macOS** | [amd64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/mac/amd64/find-large-dirs), [arm64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/mac/arm64/find-large-dirs) |
| ![FreeBSD](https://edent.github.io/SuperTinyIcons/images/svg/freebsd.svg) **FreeBSD** | [amd64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/freebsd/amd64/find-large-dirs), [arm64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/freebsd/arm64/find-large-dirs), [arm](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/freebsd/arm/find-large-dirs), [386](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/freebsd/386/find-large-dirs), [riscv64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/freebsd/riscv64/find-large-dirs) |
| **OpenBSD** | [amd64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/openbsd/amd64/find-large-dirs), [arm64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/openbsd/arm64/find-large-dirs), [arm](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/openbsd/arm/find-large-dirs), [386](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/openbsd/386/find-large-dirs), [riscv64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/openbsd/riscv64/find-large-dirs), [ppc64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/openbsd/ppc64/find-large-dirs) |
| ![Android](https://edent.github.io/SuperTinyIcons/images/svg/android.svg) **Android** | [arm64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/android/arm64/find-large-dirs) |
| **Illumos** | [amd64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/illumos/amd64/find-large-dirs) |
| **Plan9** | [amd64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/plan9/amd64/find-large-dirs), [arm](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/plan9/arm/find-large-dirs), [386](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/plan9/386/find-large-dirs) |
| **Solaris** | [amd64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/solaris/amd64/find-large-dirs) |
| **AIX** | [ppc64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/aix/ppc64/find-large-dirs) |
| **DragonFly BSD** | [amd64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/dragonfly/amd64/find-large-dirs) |
| **NetBSD** | [amd64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/netbsd/amd64/find-large-dirs), [arm64](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/netbsd/arm64/find-large-dirs), [arm](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/netbsd/arm/find-large-dirs), [386](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/netbsd/386/find-large-dirs) |
| **WASM** | [wasm](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/js/wasm/find-large-dirs), [wasi](https://files.zabiyaka.net/find-large-dirs/latest/no-gui/wasip1/wasm/find-large-dirs) |

---

## **How to Use**

**Options:**
- `--top <number>`: Display the top N largest directories (default: 20).
- `--slow-threshold <duration>`: Set a threshold for marking slow directories (default: `2s`).
- `--version`: Show the program version.
- `--exclude /path1 --exclude /path2`: Exclude /path1 /path2 from calculations.
- `--help`: Display help information.

---

Enjoy the convenience of knowing exactly where your disk space is going. 🎉
