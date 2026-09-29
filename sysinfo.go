package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Info is one snapshot of the system.
type Info struct {
	User, Host  string
	OS, OSLike  string
	Model       string
	Kernel      string
	Uptime      time.Duration
	Packages    string
	Shell       string
	WM, Session string
	Terminal    string
	TermFont    string
	Displays    []string
	GPUs        []string
	Locale      string

	CPU   CPU
	Mem   Mem
	Disks []Disk
	Net   Net
	Bat   *Battery
	Desk  Desktop
}

type CPU struct {
	Model          string
	Cores, Threads int
	MaxGHz, CurGHz float64
	Temp           float64
	Total          float64 // usage from 0 to 1
	PerCore        []float64
	NoUsage        bool // the system hides CPU times, as Android does
	Load           [3]float64
	Procs          int
}

type Mem struct{ Total, Used, SwapTotal, SwapUsed uint64 }

type Disk struct {
	Mount, FS   string
	Total, Used uint64
}

type Net struct {
	Iface, IP string
	Wifi      bool
	Signal    int // dBm
	RX, TX    uint64
}

type Battery struct {
	Capacity  int
	Status    string
	Watts     float64
	Remaining time.Duration
	Health    float64 // full capacity as a fraction of the design capacity
	Cycles    int
}

type Desktop struct {
	GTK, Icons, Cursor, Font, Mono, Scheme string
}

// collect gathers every field in parallel. The CPU usage sample sets the
// minimum run time.
func collect(sample time.Duration) *Info {
	in := &Info{}
	var wg sync.WaitGroup
	gor := func(fn func()) {
		wg.Add(1)
		go func() { defer wg.Done(); fn() }()
	}

	gor(func() { in.CPU = readCPU(sample) })
	gor(func() { in.Shell = shellVersion() })
	gor(func() { in.WM, in.Session, in.Displays = windowManager() })
	gor(func() { in.Desk = desktopSettings() })
	gor(func() { in.GPUs = gpus() })
	gor(func() { in.Terminal, in.TermFont = terminal() })
	gor(func() { in.Packages = packages() })
	gor(func() { in.Disks = disks() })
	gor(func() { in.Net = network() })
	gor(func() { in.Bat = battery() })

	in.User = os.Getenv("USER")
	in.Host, _ = os.Hostname()
	in.OS, in.OSLike = osRelease()
	in.Model = hostModel()
	in.Kernel = readTrim("/proc/sys/kernel/osrelease")
	if f := strings.Fields(readTrim("/proc/uptime")); len(f) > 0 {
		secs, _ := strconv.ParseFloat(f[0], 64)
		in.Uptime = time.Duration(secs) * time.Second
	}
	in.Mem = memory()
	in.Locale = cmpOr(os.Getenv("LC_ALL"), os.Getenv("LANG"))

	wg.Wait()
	return in
}

// ── small helpers ───────────────────────────────────────────────────────────

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readUint(path string) uint64 {
	n, _ := strconv.ParseUint(readTrim(path), 10, 64)
	return n
}

func itoa(n int) string { return strconv.Itoa(n) }

func cmpOr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// run executes a command with a short timeout and returns trimmed stdout.
func run(name string, args ...string) string {
	if _, err := exec.LookPath(name); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

var (
	versionRe      = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)
	leadingVersion = regexp.MustCompile(`^\d+(\.\d+)*`)
)

// ── identity ────────────────────────────────────────────────────────────────

func osRelease() (name, like string) {
	kv := map[string]string{}
	_ = eachLine("/etc/os-release", func(l string) {
		if k, v, ok := strings.Cut(l, "="); ok {
			kv[k] = strings.Trim(v, `"`)
		}
	})
	name = cmpOr(kv["PRETTY_NAME"], kv["NAME"], "Linux")
	ver := kv["VERSION_ID"]
	if ver == "" && kv["BUILD_ID"] != "rolling" {
		ver = kv["BUILD_ID"]
	}
	// A git describe suffix such as 4.0.0.r6646.gbf44355 adds noise.
	if v := leadingVersion.FindString(ver); v != "" {
		ver = v
	}
	if ver != "" && !strings.Contains(name, ver) {
		name += " " + ver
	}
	if f := strings.Fields(kv["ID_LIKE"]); len(f) > 0 {
		like = f[0]
	}
	return name, like
}

func hostModel() string {
	const dmi = "/sys/devices/virtual/dmi/id/"
	vendor := readTrim(dmi + "sys_vendor")
	product := readTrim(dmi + "product_name")
	if strings.EqualFold(vendor, "LENOVO") {
		if v := readTrim(dmi + "product_version"); v != "" {
			product = v
		}
	}
	if product == "" {
		// The device tree ends the name with a NUL byte.
		product = strings.TrimRight(readTrim("/sys/firmware/devicetree/base/model"), "\x00")
	}
	for _, junk := range []string{"To be filled by O.E.M.", "System Product Name", "Default string", "System manufacturer"} {
		if strings.EqualFold(product, junk) {
			product = ""
		}
		if strings.EqualFold(vendor, junk) {
			vendor = ""
		}
	}
	for _, suffix := range []string{", Inc.", " Inc.", " Corporation", " Co., Ltd.", " Computer"} {
		vendor = strings.TrimSuffix(vendor, suffix)
	}
	if vendor != "" && !strings.HasPrefix(strings.ToLower(product), strings.ToLower(vendor)) {
		product = strings.TrimSpace(vendor + " " + product)
	}
	return product
}

func shellVersion() string {
	sh := filepath.Base(os.Getenv("SHELL"))
	if sh == "" || sh == "." {
		return ""
	}
	switch sh {
	case "bash", "zsh", "fish", "nu":
		if v := versionRe.FindString(run(os.Getenv("SHELL"), "--version")); v != "" {
			return sh + " " + v
		}
	}
	return sh
}

// ── desktop ─────────────────────────────────────────────────────────────────

func windowManager() (wm, session string, displays []string) {
	session = os.Getenv("XDG_SESSION_TYPE")
	switch {
	case os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "":
		wm = "Hyprland"
		var v struct {
			Version string `json:"version"`
		}
		if json.Unmarshal([]byte(run("hyprctl", "version", "-j")), &v) == nil && v.Version != "" {
			wm += " " + v.Version
		}
		var mons []struct {
			Width   int     `json:"width"`
			Height  int     `json:"height"`
			Refresh float64 `json:"refreshRate"`
			Scale   float64 `json:"scale"`
		}
		if json.Unmarshal([]byte(run("hyprctl", "monitors", "-j")), &mons) == nil {
			for _, m := range mons {
				d := fmt.Sprintf("%dx%d @ %.0fHz", m.Width, m.Height, m.Refresh)
				if m.Scale > 0 && m.Scale != 1 {
					d += " ×" + strconv.FormatFloat(m.Scale, 'f', -1, 64)
				}
				displays = append(displays, d)
			}
		}
	case os.Getenv("NIRI_SOCKET") != "":
		wm = "niri"
	case os.Getenv("SWAYSOCK") != "":
		wm = "sway"
	default:
		wm = cmpOr(os.Getenv("XDG_CURRENT_DESKTOP"), os.Getenv("XDG_SESSION_DESKTOP"), os.Getenv("DESKTOP_SESSION"))
	}
	if len(displays) == 0 {
		displays = drmDisplays()
	}
	return wm, session, displays
}

func drmDisplays() []string {
	var out []string
	matches, _ := filepath.Glob("/sys/class/drm/card*-*/status")
	for _, st := range matches {
		if readTrim(st) != "connected" {
			continue
		}
		modes := readTrim(filepath.Join(filepath.Dir(st), "modes"))
		if m, _, _ := strings.Cut(modes, "\n"); m != "" {
			out = append(out, m)
		}
	}
	return out
}

func desktopSettings() Desktop {
	d := Desktop{}
	out := run("gsettings", "list-recursively", "org.gnome.desktop.interface")
	for l := range strings.SplitSeq(out, "\n") {
		f := strings.SplitN(l, " ", 3)
		if len(f) < 3 {
			continue
		}
		v := strings.Trim(f[2], "'")
		switch f[1] {
		case "gtk-theme":
			d.GTK = v
		case "icon-theme":
			d.Icons = v
		case "cursor-theme":
			d.Cursor = v
		case "font-name":
			d.Font = v
		case "monospace-font-name":
			d.Mono = v
		case "color-scheme":
			d.Scheme = strings.TrimPrefix(v, "prefer-")
		}
	}
	if d.GTK == "" {
		_ = eachLine(filepath.Join(home(), ".config/gtk-3.0/settings.ini"), func(l string) {
			k, v, ok := strings.Cut(l, "=")
			if !ok {
				return
			}
			v = strings.TrimSpace(v)
			switch strings.TrimSpace(k) {
			case "gtk-theme-name":
				d.GTK = v
			case "gtk-icon-theme-name":
				d.Icons = v
			case "gtk-cursor-theme-name":
				d.Cursor = v
			case "gtk-font-name":
				d.Font = v
			}
		})
	}
	if c := cmpOr(os.Getenv("HYPRCURSOR_THEME"), os.Getenv("XCURSOR_THEME")); c != "" {
		d.Cursor = c
	}
	if d.Cursor != "" {
		if s := os.Getenv("XCURSOR_SIZE"); s != "" {
			d.Cursor += " " + s
		}
	}
	if d.Mono == "" || d.Mono == "Monospace 11" {
		if m := run("fc-match", "-f", "%{family[0]}", "monospace"); m != "" {
			d.Mono = m
		}
	}
	return d
}

// terminal names the terminal emulator and the font it is configured with.
func terminal() (name, font string) {
	known := map[string]string{
		"ghostty": "ghostty", "kitty": "kitty", "alacritty": "alacritty", "foot": "foot",
		"footclient": "foot", "wezterm-gui": "wezterm", "konsole": "konsole", "xterm": "xterm",
		"gnome-terminal-": "gnome-terminal", "kgx": "console", "st": "st", "urxvt": "urxvt",
		"tilix": "tilix", "terminator": "terminator", "xfce4-terminal": "xfce4-terminal",
		"rio": "rio", "warp": "warp", "contour": "contour", "ptyxis-agent": "ptyxis",
		"cool-retro-term": "cool-retro-term", "tmux: server": "tmux", "zellij": "zellij",
	}
	if tp := os.Getenv("TERM_PROGRAM"); tp != "" && tp != "tmux" {
		name = strings.ToLower(tp)
	}
	if name == "" {
		pid := os.Getppid()
		for range 12 {
			if pid <= 1 {
				break
			}
			stat := readTrim(fmt.Sprintf("/proc/%d/stat", pid))
			open, end := strings.IndexByte(stat, '('), strings.LastIndexByte(stat, ')')
			if open < 0 || end < open {
				break
			}
			comm := stat[open+1 : end]
			if n, ok := known[comm]; ok {
				name = n
				break
			}
			rest := strings.Fields(stat[end+1:])
			if len(rest) < 2 {
				break
			}
			pid, _ = strconv.Atoi(rest[1])
		}
	}
	if name == "" {
		name = os.Getenv("TERM")
	}
	return name, terminalFont(name)
}

func terminalFont(term string) string {
	cfg := filepath.Join(home(), ".config")
	var family, size string
	grab := func(path, famKey, sizeKey, sep string) {
		_ = eachLine(path, func(l string) {
			l = strings.TrimSpace(l)
			k, v, ok := strings.Cut(l, sep)
			if !ok || strings.HasPrefix(l, "#") {
				return
			}
			k, v = strings.TrimSpace(k), unquote(v)
			if k == famKey && family == "" {
				family = v
			}
			if k == sizeKey && size == "" {
				size = v
			}
		})
	}
	switch term {
	case "ghostty":
		grab(filepath.Join(cfg, "ghostty/config"), "font-family", "font-size", "=")
		if family == "" {
			grab(filepath.Join(cfg, "ghostty/config.ghostty"), "font-family", "font-size", "=")
		}
	case "kitty":
		_ = eachLine(filepath.Join(cfg, "kitty/kitty.conf"), func(l string) {
			f := strings.Fields(l)
			if len(f) >= 2 && f[0] == "font_family" && family == "" {
				family = strings.Join(f[1:], " ")
			}
			if len(f) >= 2 && f[0] == "font_size" && size == "" {
				size = f[1]
			}
		})
	case "alacritty":
		grab(filepath.Join(cfg, "alacritty/alacritty.toml"), "family", "size", "=")
	case "foot":
		_ = eachLine(filepath.Join(cfg, "foot/foot.ini"), func(l string) {
			if v, ok := strings.CutPrefix(strings.TrimSpace(l), "font="); ok && family == "" {
				spec, _, _ := strings.Cut(v, ",")
				fam, opts, _ := strings.Cut(spec, ":")
				family = fam
				if s, ok := strings.CutPrefix(opts, "size="); ok {
					size = s
				}
			}
		})
	}
	family = strings.TrimPrefix(family, "family=")
	if family == "" {
		return ""
	}
	if size != "" {
		return family + " " + size
	}
	return family
}

func packages() string {
	var parts []string
	count := func(dir string) int {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return 0
		}
		n := 0
		for _, e := range entries {
			if e.IsDir() {
				n++
			}
		}
		return n
	}
	if n := count("/var/lib/pacman/local"); n > 0 {
		parts = append(parts, fmt.Sprintf("%d pacman", n))
	}
	if f, err := os.Open("/var/lib/dpkg/status"); err == nil {
		n := 0
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if sc.Text() == "Status: install ok installed" {
				n++
			}
		}
		f.Close()
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d dpkg", n))
		}
	}
	if n := count("/var/lib/flatpak/app") + count(filepath.Join(home(), ".local/share/flatpak/app")); n > 0 {
		parts = append(parts, fmt.Sprintf("%d flatpak", n))
	}
	if n := count("/home/linuxbrew/.linuxbrew/Cellar"); n > 0 {
		parts = append(parts, fmt.Sprintf("%d brew", n))
	}
	if n := count("/nix/var/nix/profiles/default/bin"); n > 0 {
		parts = append(parts, fmt.Sprintf("%d nix", n))
	}
	return strings.Join(parts, " · ")
}

// ── hardware ────────────────────────────────────────────────────────────────

type cpuTimes struct{ idle, total uint64 }

func readCPUTimes() []cpuTimes {
	var out []cpuTimes
	_ = eachLine("/proc/stat", func(l string) {
		if !strings.HasPrefix(l, "cpu") {
			return
		}
		f := strings.Fields(l)
		var t cpuTimes
		for i, v := range f[1:] {
			n, _ := strconv.ParseUint(v, 10, 64)
			if i >= 8 { // guest time is already inside user time
				break
			}
			t.total += n
			if i == 3 || i == 4 {
				t.idle += n
			}
		}
		out = append(out, t)
	})
	return out
}

func usage(a, b cpuTimes) float64 {
	dt := float64(b.total - a.total)
	if dt <= 0 {
		return 0
	}
	return clamp01(1 - float64(b.idle-a.idle)/dt)
}

var cpuNoise = regexp.MustCompile(`\((R|TM|tm|r)\)|\s+\d+-Core Processor|\s+(CPU|Processor)\b|\s*@.*$|\s+w/.*$|\s+with Radeon.*$`)

func readCPU(sample time.Duration) CPU {
	c := CPU{}
	before := readCPUTimes()

	cores := map[string]bool{}
	var phys, implementer string
	var armParts []string
	_ = eachLine("/proc/cpuinfo", func(l string) {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			return
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "processor":
			c.Threads++
		case "model name", "Hardware":
			if c.Model == "" {
				c.Model = v
			}
		case "physical id":
			phys = v
		case "core id":
			cores[phys+":"+v] = true
		case "CPU implementer":
			implementer = v
		case "CPU part":
			if name := armCore(implementer, v); !slices.Contains(armParts, name) {
				armParts = append(armParts, name)
			}
		}
	})
	// ARM boards such as the Raspberry Pi name the cores only by part
	// number. A big.LITTLE chip lists each core type.
	if len(armParts) > 0 && (c.Model == "" || strings.HasPrefix(c.Model, "ARMv")) {
		c.Model = strings.Join(armParts, " + ")
	}
	c.Model = strings.Join(strings.Fields(cpuNoise.ReplaceAllString(c.Model, "")), " ")
	if c.Model == "" {
		c.Model = runtime.GOARCH
	}
	c.Cores = len(cores)
	if c.Cores == 0 {
		c.Cores = c.Threads
	}
	c.MaxGHz = float64(readUint("/sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq")) / 1e6
	c.Temp = cpuTemp()
	c.Load, c.Procs = loadAvg()

	c.NoUsage = len(before) == 0
	time.Sleep(sample)
	after := readCPUTimes()
	if len(before) > 0 && len(after) == len(before) {
		c.Total = usage(before[0], after[0])
		for i := 1; i < len(after); i++ {
			c.PerCore = append(c.PerCore, usage(before[i], after[i]))
		}
	}
	c.CurGHz = curFreq()
	return c
}

// armCores names the ARM Ltd core designs by their CPU part number.
var armCores = map[string]string{
	"0xc07": "Cortex-A7", "0xc08": "Cortex-A8", "0xc09": "Cortex-A9", "0xc0d": "Cortex-A12",
	"0xc0e": "Cortex-A17", "0xc0f": "Cortex-A15", "0xd02": "Cortex-A34", "0xd03": "Cortex-A53",
	"0xd04": "Cortex-A35", "0xd05": "Cortex-A55", "0xd06": "Cortex-A65", "0xd07": "Cortex-A57",
	"0xd08": "Cortex-A72", "0xd09": "Cortex-A73", "0xd0a": "Cortex-A75", "0xd0b": "Cortex-A76",
	"0xd0c": "Neoverse-N1", "0xd0d": "Cortex-A77", "0xd0e": "Cortex-A76AE", "0xd40": "Neoverse-V1",
	"0xd41": "Cortex-A78", "0xd42": "Cortex-A78AE", "0xd44": "Cortex-X1", "0xd46": "Cortex-A510",
	"0xd47": "Cortex-A710", "0xd48": "Cortex-X2", "0xd49": "Neoverse-N2", "0xd4b": "Cortex-A78C",
	"0xd4d": "Cortex-A715", "0xd4e": "Cortex-X3", "0xd4f": "Neoverse-V2", "0xd80": "Cortex-A520",
	"0xd81": "Cortex-A720", "0xd82": "Cortex-X4",
}

// armCore names one core from its implementer and part number.
func armCore(implementer, part string) string {
	part = strings.ToLower(part)
	if implementer == "0x41" {
		if name, ok := armCores[part]; ok {
			return name
		}
	}
	vendor := map[string]string{"0x41": "ARM", "0x42": "Broadcom", "0x48": "HiSilicon", "0x4e": "NVIDIA",
		"0x51": "Qualcomm", "0x53": "Samsung", "0x61": "Apple", "0xc0": "Ampere"}[implementer]
	return strings.TrimSpace(cmpOr(vendor, "ARM") + " " + part)
}

// loadAvg returns the 1, 5 and 15 minute load and the number of processes.
func loadAvg() (load [3]float64, procs int) {
	f := strings.Fields(readTrim("/proc/loadavg"))
	if len(f) < 4 {
		return load, 0
	}
	for i := range 3 {
		load[i], _ = strconv.ParseFloat(f[i], 64)
	}
	if _, total, ok := strings.Cut(f[3], "/"); ok {
		procs, _ = strconv.Atoi(total)
	}
	return load, procs
}

func curFreq() float64 {
	matches, _ := filepath.Glob("/sys/devices/system/cpu/cpu[0-9]*/cpufreq/scaling_cur_freq")
	var sum, n float64
	for _, m := range matches {
		if v := readUint(m); v > 0 {
			sum += float64(v)
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / n / 1e6
}

func cpuTemp() float64 {
	prio := []string{"coretemp", "k10temp", "zenpower", "cpu_thermal", "soc_thermal", "acpitz"}
	best, bestRank := 0.0, len(prio)
	mons, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	for _, m := range mons {
		rank := slices.Index(prio, readTrim(filepath.Join(m, "name")))
		if rank < 0 || rank >= bestRank {
			continue
		}
		input := filepath.Join(m, "temp1_input")
		labels, _ := filepath.Glob(filepath.Join(m, "temp*_label"))
		for _, l := range labels {
			switch readTrim(l) {
			case "Tctl", "Tdie", "Package id 0":
				input = strings.TrimSuffix(l, "_label") + "_input"
			}
		}
		if v := readUint(input); v > 0 {
			best, bestRank = float64(v)/1000, rank
		}
	}
	return best
}

func gpus() []string {
	var out []string
	devs, _ := filepath.Glob("/sys/bus/pci/devices/*")
	for _, d := range devs {
		if !strings.HasPrefix(readTrim(filepath.Join(d, "class")), "0x03") {
			continue
		}
		vendor := strings.TrimPrefix(readTrim(filepath.Join(d, "vendor")), "0x")
		device := strings.TrimPrefix(readTrim(filepath.Join(d, "device")), "0x")
		name := pciName(vendor, device)
		short := map[string]string{"8086": "Intel", "10de": "NVIDIA", "1002": "AMD", "1af4": "Virtio", "15ad": "VMware"}[vendor]
		if i, j := strings.IndexByte(name, '['), strings.LastIndexByte(name, ']'); i >= 0 && j > i {
			name = name[i+1 : j]
		}
		if name == "" {
			name = "GPU " + vendor + ":" + device
		}
		if short != "" && !strings.HasPrefix(name, short) {
			name = short + " " + name
		}
		out = append(out, name)
	}
	return out
}

func pciName(vendor, device string) string {
	for _, p := range []string{"/usr/share/hwdata/pci.ids", "/usr/share/misc/pci.ids", "/usr/share/pci.ids"} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		inVendor := false
		for sc.Scan() {
			l := sc.Text()
			if l == "" || l[0] == '#' {
				continue
			}
			if l[0] != '\t' {
				if inVendor {
					return ""
				}
				inVendor = strings.HasPrefix(l, vendor+"  ")
				continue
			}
			if inVendor && len(l) > 1 && l[1] != '\t' && strings.HasPrefix(l[1:], device+"  ") {
				return strings.TrimSpace(l[1+len(device):])
			}
		}
		return ""
	}
	return ""
}

func memory() Mem {
	kv := map[string]uint64{}
	_ = eachLine("/proc/meminfo", func(l string) {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			return
		}
		n, _ := strconv.ParseUint(strings.Fields(v)[0], 10, 64)
		kv[k] = n * 1024
	})
	m := Mem{Total: kv["MemTotal"], SwapTotal: kv["SwapTotal"]}
	if kv["MemAvailable"] <= m.Total {
		m.Used = m.Total - kv["MemAvailable"]
	}
	if kv["SwapFree"] <= m.SwapTotal {
		m.SwapUsed = m.SwapTotal - kv["SwapFree"]
	}
	return m
}

func disks() []Disk {
	fsTypes := map[string]bool{
		"ext2": true, "ext3": true, "ext4": true, "btrfs": true, "xfs": true, "f2fs": true,
		"bcachefs": true, "zfs": true, "ntfs": true, "ntfs3": true, "exfat": true, "jfs": true,
		"reiserfs": true,
	}
	byDev := map[string]Disk{}
	var order []string
	_ = eachLine("/proc/self/mounts", func(l string) {
		f := strings.Fields(l)
		if len(f) < 3 || !fsTypes[f[2]] {
			return
		}
		dev, mnt := f[0], strings.ReplaceAll(f[1], `\040`, " ")
		if strings.HasPrefix(dev, "/dev/loop") || strings.HasPrefix(mnt, "/boot") ||
			strings.HasPrefix(mnt, "/efi") || strings.HasPrefix(mnt, "/snap") || strings.HasPrefix(mnt, "/var/lib/docker") {
			return
		}
		if prev, ok := byDev[dev]; ok && len(prev.Mount) <= len(mnt) {
			return
		}
		var st syscall.Statfs_t
		if syscall.Statfs(mnt, &st) != nil || st.Blocks == 0 {
			return
		}
		bs := uint64(st.Bsize)
		used := (st.Blocks - st.Bfree) * bs
		if _, ok := byDev[dev]; !ok {
			order = append(order, dev)
		}
		byDev[dev] = Disk{Mount: mnt, FS: f[2], Total: used + st.Bavail*bs, Used: used}
	})
	var out []Disk
	for _, dev := range order {
		out = append(out, byDev[dev])
	}
	slices.SortStableFunc(out, func(a, b Disk) int { return len(a.Mount) - len(b.Mount) })
	return out
}

func network() Net {
	n := Net{}
	best := uint64(1 << 62)
	_ = eachLine("/proc/net/route", func(l string) {
		f := strings.Fields(l)
		if len(f) < 7 || f[1] != "00000000" {
			return
		}
		metric, _ := strconv.ParseUint(f[6], 10, 64)
		if metric < best {
			best, n.Iface = metric, f[0]
		}
	})
	if n.Iface == "" {
		return n
	}
	if ifc, err := net.InterfaceByName(n.Iface); err == nil {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				n.IP = ipn.IP.String()
				break
			}
		}
	}
	_ = eachLine("/proc/net/wireless", func(l string) {
		f := strings.Fields(l)
		if len(f) >= 4 && strings.TrimSuffix(f[0], ":") == n.Iface {
			n.Wifi = true
			lvl, _ := strconv.ParseFloat(strings.TrimSuffix(f[3], "."), 64)
			n.Signal = int(lvl)
		}
	})
	n.RX, n.TX = netBytes(n.Iface)
	return n
}

func netBytes(iface string) (rx, tx uint64) {
	base := "/sys/class/net/" + iface + "/statistics/"
	return readUint(base + "rx_bytes"), readUint(base + "tx_bytes")
}

func battery() *Battery {
	bats, _ := filepath.Glob("/sys/class/power_supply/BAT*")
	if len(bats) == 0 {
		return nil
	}
	p := bats[0] + "/"
	b := &Battery{Status: strings.ToLower(readTrim(p + "status"))}
	b.Capacity, _ = strconv.Atoi(readTrim(p + "capacity"))

	now, full := float64(readUint(p+"energy_now")), float64(readUint(p+"energy_full"))
	watts := float64(readUint(p+"power_now")) / 1e6
	if now == 0 {
		// charge_* files use µAh, so convert with the voltage.
		volt := float64(readUint(p+"voltage_now")) / 1e6
		now = float64(readUint(p+"charge_now")) * volt
		full = float64(readUint(p+"charge_full")) * volt
		watts = float64(readUint(p+"current_now")) / 1e6 * volt
	}
	b.Watts = watts
	// Health compares like with like: energy with energy, charge with charge.
	for _, kind := range []string{"energy", "charge"} {
		f, d := float64(readUint(p+kind+"_full")), float64(readUint(p+kind+"_full_design"))
		if f > 0 && d > 0 {
			b.Health = min(1, f/d)
			break
		}
	}
	b.Cycles, _ = strconv.Atoi(readTrim(p + "cycle_count"))
	if watts > 0.1 {
		switch b.Status {
		case "discharging":
			b.Remaining = time.Duration(now / 1e6 / watts * float64(time.Hour))
		case "charging":
			b.Remaining = time.Duration((full - now) / 1e6 / watts * float64(time.Hour))
		}
	}
	return b
}
