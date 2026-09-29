package main

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const historySize = 512

// liveState keeps the counters that rates and usage are computed from.
type liveState struct {
	cpu    []cpuTimes
	rx, tx uint64
	at     time.Time
	hist   history
	tick   int
}

func (s *liveState) update(in *Info) {
	now := time.Now()
	cur := readCPUTimes()
	in.CPU.NoUsage = len(cur) == 0
	if len(s.cpu) == len(cur) && len(cur) > 0 {
		in.CPU.Total = usage(s.cpu[0], cur[0])
		in.CPU.PerCore = in.CPU.PerCore[:0]
		for i := 1; i < len(cur); i++ {
			in.CPU.PerCore = append(in.CPU.PerCore, usage(s.cpu[i], cur[i]))
		}
	}
	s.cpu = cur
	in.CPU.CurGHz = curFreq()
	in.CPU.Temp = cpuTemp()
	in.CPU.Load, in.CPU.Procs = loadAvg()
	in.Mem = memory()
	if f := strings.Fields(readTrim("/proc/uptime")); len(f) > 0 {
		if d, err := time.ParseDuration(f[0] + "s"); err == nil {
			in.Uptime = d
		}
	}

	s.tick++
	if s.tick%5 == 0 {
		in.Disks = disks()
		in.Bat = battery()
		in.Desk = desktopSettings()
	}
	if s.tick%30 == 0 {
		in.Packages = packages()
		in.WM, in.Session, in.Displays = windowManager()
	}
	if s.tick%10 == 0 {
		iface := in.Net.Iface
		in.Net = network()
		if in.Net.Iface != iface {
			s.rx, s.tx = in.Net.RX, in.Net.TX
		}
	}

	rx, tx := netBytes(in.Net.Iface)
	if dt := now.Sub(s.at).Seconds(); !s.at.IsZero() && dt > 0 {
		s.hist.rx = push(s.hist.rx, rateOf(s.rx, rx, dt))
		s.hist.tx = push(s.hist.tx, rateOf(s.tx, tx, dt))
	}
	s.rx, s.tx, s.at = rx, tx, now
	in.Net.RX, in.Net.TX = rx, tx
	s.hist.cpu = push(s.hist.cpu, in.CPU.Total)
}

func rateOf(prev, cur uint64, dt float64) float64 {
	if cur < prev {
		return 0
	}
	return float64(cur-prev) / dt
}

func push(v []float64, x float64) []float64 {
	v = append(v, x)
	if len(v) > historySize {
		v = v[len(v)-historySize:]
	}
	return v
}

// themeStamp changes when the theme needs a reload: the color file changed,
// or, for the active theme, the theme or the wallpaper changed.
func themeStamp(spec string, t *Theme) string {
	var b strings.Builder
	if t.File != "" {
		if st, err := os.Stat(t.File); err == nil {
			fmt.Fprintf(&b, "%s|%d|%d", t.File, st.Size(), st.ModTime().UnixNano())
		}
	}
	if spec == "" {
		_, name, wall := currentThemeDir()
		b.WriteString("|" + name + "|" + wall)
	}
	return b.String()
}

// runLive draws the dashboard and redraws it on every interval, as btop
// does. It also reloads the theme when the theme changes on disk.
func runLive(o options, t *Theme) error {
	interval := time.Duration(max(250, o.interval)) * time.Millisecond
	in := collect(120 * time.Millisecond)
	st := &liveState{}
	st.cpu = readCPUTimes()
	st.rx, st.tx, st.at = in.Net.RX, in.Net.TX, time.Now()
	st.hist.cpu = []float64{in.CPU.Total}
	stamp := themeStamp(o.theme, t)

	out := bufio.NewWriterSize(os.Stdout, 1<<16)
	restore := func() {}
	if ts, ok := rawMode(os.Stdin, 1, 0); ok {
		restore = ts.restore
	}
	out.WriteString("\x1b[?1049h\x1b[?25l")
	out.Flush()
	defer func() {
		out.WriteString("\x1b[0m\x1b[?25h\x1b[?1049l")
		out.Flush()
		restore()
	}()

	keys := make(chan byte, 8)
	go func() {
		buf := make([]byte, 16)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				close(keys)
				return
			}
			// A lone escape is the Esc key. Longer sequences are other keys.
			if n == 1 || buf[0] != 0x1b {
				keys <- buf[0]
			}
		}
	}()
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGWINCH)
	defer signal.Stop(sig)

	var prev []string
	lastW, lastH := 0, 0
	draw := func() {
		cols, rows := termSize()
		w := cols
		if o.width > 0 {
			w = min(o.width, cols)
		}
		lay := layout{
			hist:  &st.hist,
			clock: time.Now().Format("15:04:05"),
			hint:  fmt.Sprintf("- %dms + · q quit", interval.Milliseconds()),
			d:     detail{cpuH: 4, netH: 2},
		}
		if o.noLogo {
			lay.d.logo = 2
		}
		lines := dashboard(t, in, w, rows, lay)

		// Send only the lines that changed since the last frame. A new size
		// or a new theme repaints the whole screen.
		if cols != lastW || rows != lastH {
			prev, lastW, lastH = nil, cols, rows
		}
		if prev == nil {
			out.WriteString(rst() + "\x1b[2J")
		}
		next := make([]string, len(lines))
		for i, l := range lines {
			next[i] = compactSGR(rst() + l + rst())
			if i < len(prev) && prev[i] == next[i] {
				continue
			}
			// A line that fills the row leaves the cursor on the last cell,
			// where an erase would delete the right border.
			el := "\x1b[K"
			if vlen(next[i]) >= cols {
				el = ""
			}
			fmt.Fprintf(out, "\x1b[%d;1H%s%s", i+1, next[i], el)
		}
		if len(prev) > len(next) {
			fmt.Fprintf(out, "\x1b[%d;1H%s\x1b[J", len(next)+1, rst())
		}
		prev = next
		out.Flush()
	}

	reload := func() {
		next := themeStamp(o.theme, t)
		if next == stamp {
			return
		}
		stamp = next
		nt, err := loadTheme(o.theme, false)
		if err != nil {
			return
		}
		t = nt
		setCanvas(o, t)
		in.Desk = desktopSettings()
		in.Terminal, in.TermFont = terminal()
		prev = nil
	}

	draw()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			st.update(in)
			reload()
			draw()
		case k, ok := <-keys:
			switch {
			case !ok || k == 'q' || k == 'Q' || k == 0x1b || k == 3:
				return nil
			case k == '+' || k == '=':
				interval = min(interval+250*time.Millisecond, 10*time.Second)
				ticker.Reset(interval)
				draw()
			case k == '-' || k == '_':
				interval = max(interval-250*time.Millisecond, 250*time.Millisecond)
				ticker.Reset(interval)
				draw()
			}
		case s := <-sig:
			if s != syscall.SIGWINCH {
				return nil
			}
			prev = nil
			draw()
		}
	}
}
