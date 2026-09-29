package main

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestParseColor(t *testing.T) {
	cases := map[string]RGB{
		"#abc":            {0xaa, 0xbb, 0xcc},
		"#12121c":         {0x12, 0x12, 0x1c},
		"#7dcfffee":       {0x7d, 0xcf, 0xff},
		"0xFF7EDB":        {0xff, 0x7e, 0xdb},
		"'#000000'":       {0, 0, 0},
		"rgb(1, 2, 3)":    {1, 2, 3},
		"rgba(7dcfffee)":  {0x7d, 0xcf, 0xff},
		"rgb(300, -4, 9)": {255, 0, 9},
	}
	for in, want := range cases {
		got, ok := parseColor(in)
		if !ok || got != want {
			t.Errorf("parseColor(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "#12", "nope", "rgb(1,2)", "#zzzzzz"} {
		if _, ok := parseColor(bad); ok {
			t.Errorf("parseColor(%q) succeeded; want failure", bad)
		}
	}
}

func TestContrast(t *testing.T) {
	if r := contrast(RGB{}, RGB{255, 255, 255}); math.Abs(r-21) > 0.01 {
		t.Errorf("black on white = %.2f; want 21", r)
	}
	if r := contrast(RGB{10, 20, 30}, RGB{10, 20, 30}); r != 1 {
		t.Errorf("same color = %.2f; want 1", r)
	}
	if g := wcagGrade(4.6); g != "AA" {
		t.Errorf("grade 4.6 = %s; want AA", g)
	}
	if f := contrastFrac(21); f != 1 {
		t.Errorf("contrastFrac(21) = %f; want 1", f)
	}
}

func TestBlendEnds(t *testing.T) {
	a, b := RGB{255, 0, 0}, RGB{0, 0, 255}
	if blend(a, b, 0) != a || blend(a, b, 1) != b {
		t.Errorf("blend ends: %v %v", blend(a, b, 0), blend(a, b, 1))
	}
	if gradient([]RGB{a, b}, 1) != b {
		t.Errorf("gradient end = %v; want %v", gradient([]RGB{a, b}, 1), b)
	}
}

// The fallbacks must match omarchy-theme-color, so that pixeltop shows the
// colors that Omarchy writes into app configs.
func TestResolvePaletteFallbacks(t *testing.T) {
	p, mode := resolvePalette(map[string]string{
		"background": "#ffffff", "foreground": "#000000",
		"red": "#aa0000", "yellow": "#aaaa00", "green": "#00aa00",
		"cyan": "#00aaaa", "blue": "#0000aa", "purple": "#aa00aa",
	}, "")
	if mode != "light" {
		t.Errorf("mode = %s; want light from the background luminance", mode)
	}
	if p.Orange != p.Yellow {
		t.Errorf("orange = %v; want yellow %v", p.Orange, p.Yellow)
	}
	if want := mix(p.Orange, RGB{}, 0.5); p.Brown != want {
		t.Errorf("brown = %v; want %v", p.Brown, want)
	}
	if want := mix(p.Red, RGB{255, 255, 255}, 0.2); p.BrightRed != want {
		t.Errorf("bright red = %v; want %v", p.BrightRed, want)
	}
	if want := mix(p.Background, RGB{}, 0.25); p.DarkBackground != want {
		t.Errorf("dark background = %v; want %v", p.DarkBackground, want)
	}
	if p.Magenta != (RGB{0xaa, 0, 0xaa}) {
		t.Errorf("magenta = %v; want the purple alias", p.Magenta)
	}
	if p.Accent != p.Blue {
		t.Errorf("accent = %v; want blue", p.Accent)
	}
}

func TestResolvePaletteLegacyNames(t *testing.T) {
	p, mode := resolvePalette(map[string]string{
		"color0": "#101010", "color7": "#e0e0e0", "color1": "#ff0000", "color8": "#555555",
		"mode": "dark",
	}, "")
	if mode != "dark" || p.Background != (RGB{0x10, 0x10, 0x10}) || p.Red != (RGB{255, 0, 0}) {
		t.Errorf("legacy palette resolved to %v %v %v", mode, p.Background, p.Red)
	}
	if p.Muted != (RGB{0x55, 0x55, 0x55}) {
		t.Errorf("muted = %v; want color8", p.Muted)
	}
}

func TestParseThemeFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	toml := write("colors.toml", "# comment\nmode = \"dark\"\naccent = \"#ff7edb\" # inline\nbackground = '#12121c'\n")
	raw, err := parseColorsToml(toml)
	if err != nil || raw["accent"] != "#ff7edb" || raw["background"] != "#12121c" || raw["mode"] != "dark" {
		t.Errorf("colors.toml = %v, %v", raw, err)
	}

	ala := write("alacritty.toml", "[colors.primary]\nbackground = '#1a1b26'\nforeground = \"#a9b1d6\"\n\n[colors.normal]\nred = '0xf7768e'\n\n[colors.bright]\nblue = '#7aa2f7'\n")
	raw, _ = parseAlacritty(ala)
	if raw["background"] != "#1a1b26" || raw["color1"] != "0xf7768e" || raw["color12"] != "#7aa2f7" {
		t.Errorf("alacritty.toml = %v", raw)
	}

	kitty := write("kitty.conf", "background #000000\ncolor1 #ff0000\nactive_border_color #00ff00\n")
	raw, _ = parseKitty(kitty)
	if raw["background"] != "#000000" || raw["color1"] != "#ff0000" || raw["accent"] != "#00ff00" {
		t.Errorf("kitty.conf = %v", raw)
	}

	ghost := write("ghostty.conf", "background = 101010\npalette = 1=#ff0000\n")
	raw, _ = parseGhostty(ghost)
	if raw["background"] != "#101010" || raw["color1"] != "#ff0000" {
		t.Errorf("ghostty.conf = %v", raw)
	}
}

func TestParseOSCReplies(t *testing.T) {
	reply := "\x1b]10;rgb:c0c0/caca/f5f5\x1b\\" +
		"\x1b]11;rgb:1212/1212/1c1c\x07" +
		"\x1b]4;1;rgb:f7/76/8e\x1b\\" +
		"\x1b]4;8;rgb:3b3b/4242/6161\x1b\\" +
		"\x1b[?62;22c"
	got := parseOSCReplies(reply)
	want := map[string]string{
		"foreground": "#c0caf5", "background": "#12121c", "color1": "#f7768e",
		"color8": "#3b4261", "muted": "#3b4261",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q; want %q", k, got[k], v)
		}
	}
	if parseOSCReplies("\x1b[?62c") != nil {
		t.Error("a terminal without OSC replies must give nil")
	}
}

func TestVisibleWidth(t *testing.T) {
	cases := map[string]int{
		"plain":                              5,
		"\x1b[38;2;1;2;3mred\x1b[0m":         3,
		"╭─┐¹cpu┌─╮":                         10,
		"日本":                                 4,
		"\x1b]8;;https://x\x1b\\link\x1b[0m": 4,
	}
	for s, want := range cases {
		if got := vlen(s); got != want {
			t.Errorf("vlen(%q) = %d; want %d", s, got, want)
		}
	}
	if got := trunc("abcdefgh", 5); got != "abcd…" || vlen(got) != 5 {
		t.Errorf("trunc = %q", got)
	}
}

func TestGraph(t *testing.T) {
	depth = depthNone
	defer func() { depth = depthTrue }()
	th := builtinTheme()
	lines := graph([]float64{1, 0}, 1, 1, th.heat(), th)
	// The left column is full and the right column is empty.
	if lines[0] != "⡇" {
		t.Errorf("graph = %q; want ⡇", lines[0])
	}
	lines = graph([]float64{1, 1}, 1, 2, th.heat(), th)
	if lines[0] != "⣿" || lines[1] != "⣿" {
		t.Errorf("full graph = %q", lines)
	}
}

func fakeInfo() *Info {
	return &Info{
		User: "user", Host: "host", OS: "Omarchy 4.0.0", OSLike: "arch",
		Model: "Framework Laptop 13", Kernel: "7.2.3-arch1-1", Uptime: 90 * time.Minute,
		Packages: "1049 pacman · 12 flatpak", Shell: "bash 5.3", WM: "Hyprland 0.56", Session: "wayland",
		Terminal: "ghostty", TermFont: "JetBrainsMono Nerd Font 9", Displays: []string{"2880x1800 @ 120Hz"},
		GPUs: []string{"Intel Arc B390"},
		CPU: CPU{
			Model: "Intel Core Ultra X7 358H", Cores: 16, Threads: 16, CurGHz: 2.1, Temp: 55,
			Total: 0.23, PerCore: []float64{0.1, 0.5, 0.9, 0.2, 0.1, 0.3, 0.6, 0.1, 0.1, 0.5, 0.9, 0.2, 0.1, 0.3, 0.6, 0.1},
			Load: [3]float64{0.5, 0.6, 0.7}, Procs: 412,
		},
		Mem:   Mem{Total: 32 << 30, Used: 12 << 30, SwapTotal: 8 << 30, SwapUsed: 1 << 30},
		Disks: []Disk{{Mount: "/", Total: 1 << 40, Used: 200 << 30}, {Mount: "/home/user/a/very/long/mount", Total: 1 << 40, Used: 1 << 39}},
		Net:   Net{Iface: "wlan0", IP: "192.168.1.20", Wifi: true, Signal: -55, RX: 5 << 30, TX: 1 << 30},
		Bat:   &Battery{Capacity: 80, Status: "discharging", Watts: 7.5, Remaining: 3 * time.Hour, Health: 0.94, Cycles: 120},
		Desk:  Desktop{GTK: "Adwaita-dark", Icons: "Yaru-blue", Cursor: "default", Font: "Adwaita Sans 11"},
	}
}

var wellFormed = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// plainWidth measures a line without the renderer's own helpers: it removes
// well-formed escape codes and counts the runes. A broken code leaves an
// escape byte behind, which fails the check.
func plainWidth(t *testing.T, l string) int {
	t.Helper()
	plain := wellFormed.ReplaceAllString(l, "")
	if strings.ContainsRune(plain, 0x1b) {
		t.Fatalf("broken escape code in %q", l)
	}
	n := 0
	for _, r := range plain {
		n += runeWidth(r)
	}
	return n
}

// Every frame must fit the terminal, and all lines of a frame must have one
// width, or the borders do not line up.
func TestDashboardFitsEveryTerminal(t *testing.T) {
	th := builtinTheme()
	hist := &history{cpu: []float64{0.1, 0.5, 0.9}, rx: []float64{1e3, 5e5}, tx: []float64{2e3, 1e4}}
	live := layout{hist: hist, clock: "12:00:00", hint: "- 1000ms + · q quit", d: detail{cpuH: 4, netH: 2}}
	noBat := fakeInfo()
	noBat.Bat = nil
	infos := map[string]*Info{"battery": fakeInfo(), "no battery": noBat}

	for name, in := range infos {
		for _, w := range []int{24, 27, 30, 36, 44, 52, 60, 70, 77, 78, 89, 90, 99, 100, 119, 120, 139, 140, 160, 199, 200, 260} {
			for _, rows := range []int{0, 6, 8, 12, 16, 20, 24, 30, 40, 60} {
				for _, lay := range []layout{live, {d: detail{logo: 2}}, {}} {
					if lay.hist == nil && rows > 0 && rows != 24 {
						continue
					}
					lines := dashboard(th, in, w, rows, lay)
					if rows > 0 && len(lines) > rows {
						t.Fatalf("%s %dx%d: %d lines", name, w, rows, len(lines))
					}
					width := plainWidth(t, lines[0])
					for i, l := range lines {
						if got := plainWidth(t, l); got != width || got > w {
							t.Fatalf("%s %dx%d: line %d is %d cells, line 0 is %d: %q", name, w, rows, i, got, width, l)
						}
					}
				}
			}
		}
	}
}

func TestSmallTerminalsKeepTheKeyBoxes(t *testing.T) {
	th := builtinTheme()
	hist := &history{cpu: []float64{0.4}, rx: []float64{1}, tx: []float64{1}}
	live := layout{hist: hist, d: detail{cpuH: 4, netH: 2}}
	for _, size := range [][2]int{{80, 30}, {60, 30}, {100, 20}, {44, 34}, {140, 16}} {
		out := strings.Join(dashboard(th, fakeInfo(), size[0], size[1], live), "\n")
		for _, title := range []string{"theme", "cpu", "mem"} {
			if !strings.Contains(out, title) {
				t.Errorf("%dx%d: the %s box is missing", size[0], size[1], title)
			}
		}
	}
}

func TestWideShortTerminalsUseMoreColumns(t *testing.T) {
	th := builtinTheme()
	live := layout{hist: &history{cpu: []float64{0.4}, rx: []float64{1}, tx: []float64{1}}, d: detail{cpuH: 4, netH: 2}}
	first := dashboard(th, fakeInfo(), 200, 18, live)[0]
	if n := strings.Count(first, "╭"); n < 3 {
		t.Errorf("200x18 has %d columns; want 3 or more", n)
	}
	first = dashboard(th, fakeInfo(), 110, 60, live)[0]
	if n := strings.Count(first, "╭"); n != 2 {
		t.Errorf("110x60 has %d columns; want 2", n)
	}
}

// Below the two column breakpoint, the dashboard stacks one box per row.
func TestSmallScreensCollapseToOneColumn(t *testing.T) {
	depth = depthNone
	defer func() { depth = depthTrue }()
	th := builtinTheme()
	live := layout{hist: &history{cpu: []float64{0.4}, rx: []float64{1}, tx: []float64{1}}, d: detail{cpuH: 4, netH: 2}}
	cases := []struct{ w, rows int }{
		{24, 40}, {40, 0}, {40, 30}, {60, 60}, {80, 24}, {shortTwoColumns - 1, 20},
		{twoColumns - 1, 0}, {twoColumns - 1, 70},
	}
	for _, c := range cases {
		for _, l := range dashboard(th, fakeInfo(), c.w, c.rows, live) {
			if n := strings.Count(l, "╭"); n > 1 {
				t.Fatalf("%dx%d: %d boxes side by side: %q", c.w, c.rows, n, l)
			}
		}
	}
	if n := strings.Count(dashboard(th, fakeInfo(), twoColumns, 60, live)[0], "╭"); n != 2 {
		t.Errorf("width %d has %d columns; want 2", twoColumns, n)
	}
	// A short terminal, such as a phone in landscape, uses two columns when
	// one column would drop boxes.
	if n := strings.Count(dashboard(th, fakeInfo(), 96, 20, live)[0], "╭"); n != 2 {
		t.Errorf("96x20 has %d columns; want 2", n)
	}
}

func TestTooSmall(t *testing.T) {
	depth = depthNone
	defer func() { depth = depthTrue }()
	th := builtinTheme()
	for _, size := range [][2]int{{minWidth - 1, 30}, {80, minHeight - 1}} {
		out := strings.Join(dashboard(th, fakeInfo(), size[0], size[1], layout{}), "\n")
		if !strings.Contains(strings.Join(strings.Fields(out), " "), "Make the terminal larger.") {
			t.Errorf("%dx%d: no size message in %q", size[0], size[1], out)
		}
	}
}

func TestPaletteFitsNarrowWidths(t *testing.T) {
	th := builtinTheme()
	for _, w := range []int{30, 40, 50, 60, 70, 96} {
		for i, l := range paletteView(th, w) {
			if got := vlen(l); got != w {
				t.Fatalf("palette width %d, line %d is %d cells: %q", w, i, got, l)
			}
		}
	}
}

func TestParseArgs(t *testing.T) {
	o, pos, err := parseArgs([]string{"themes", "-w", "90", "kanagawa", "--theme", "nord", "--once"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pos, ",") != "themes,kanagawa" || o.width != 90 || o.theme != "nord" || !o.once {
		t.Errorf("parseArgs = %+v %v", o, pos)
	}
}

func TestOSVersionTrim(t *testing.T) {
	if v := leadingVersion.FindString("4.0.0.r6646.gbf44355"); v != "4.0.0" {
		t.Errorf("version = %q", v)
	}
}

func TestLogoGrid(t *testing.T) {
	depth = depthNone
	defer func() { depth = depthTrue }()
	for _, row := range omarchyLogo {
		if len(row) != 13 {
			t.Fatalf("logo row %q is not 13 pixels", row)
		}
	}
	// At scale 2 each logo pixel is two cells wide and one row tall.
	lines := logoLines(nil, 30, 2)
	if len(lines) != 13 {
		t.Fatalf("scale 2 logo has %d rows; want 13", len(lines))
	}
	if strings.TrimSpace(lines[0]) != strings.Repeat("█", 26) {
		t.Errorf("top row = %q", lines[0])
	}
	if got := strings.TrimRight(lines[12], " "); got != "  "+strings.Repeat("█", 14)+"  "+strings.Repeat("█", 10) {
		t.Errorf("bottom row = %q", got)
	}
	for _, l := range lines {
		if vlen(l) > 30 {
			t.Errorf("logo line wider than 30 cells: %q", l)
		}
	}
	// At scale 1 half blocks put two logo rows in one text row.
	if n := len(logoLines(nil, 13, 1)); n != 7 {
		t.Errorf("scale 1 logo has %d rows; want 7", n)
	}
}

func TestThemeStampChangesWithFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "colors.toml")
	if err := os.WriteFile(p, []byte("background = \"#000000\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	th, err := themeFromFile(p)
	if err != nil {
		t.Fatal(err)
	}
	before := themeStamp(p, th)
	if err := os.WriteFile(p, []byte("background = \"#111111\"\nred = \"#ff0000\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if themeStamp(p, th) == before {
		t.Error("the stamp did not change after an edit of the color file")
	}
}

type cell struct {
	r        rune
	fg, bg   string
	boldCell bool
}

// cells replays the color codes of a line the way a terminal does.
func cells(s string) []cell {
	var out []cell
	var fgc, bgc string
	b := false
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := skipEscape(s, i)
			seq := s[i:j]
			i = j
			if !strings.HasSuffix(seq, "m") {
				continue
			}
			switch p := seq[2 : len(seq)-1]; {
			case p == "0" || p == "":
				fgc, bgc, b = "", "", false
			case strings.HasPrefix(p, "38;"):
				fgc = p
			case strings.HasPrefix(p, "48;"):
				bgc = p
			case p == "1":
				b = true
			case p == "22":
				b = false
			}
			continue
		}
		r, size := decodeRune(s[i:])
		out = append(out, cell{r, fgc, bgc, b})
		i += size
	}
	return out
}

func TestCompactSGRKeepsTheLook(t *testing.T) {
	th := builtinTheme()
	c := th.Background
	hist := &history{cpu: []float64{0.2, 0.7}, rx: []float64{1e4, 3e5}, tx: []float64{5e3, 1e4}}
	for _, paint := range []*RGB{nil, &c} {
		canvas = paint
		for _, w := range []int{40, 120} {
			for _, l := range dashboard(th, fakeInfo(), w, 40, layout{hist: hist, d: detail{cpuH: 3, netH: 2}}) {
				full := rst() + l + rst()
				small := compactSGR(full)
				if len(small) > len(full) {
					t.Fatalf("compacted line is longer")
				}
				a, b := cells(full), cells(small)
				if len(a) != len(b) {
					t.Fatalf("cell count %d, want %d", len(b), len(a))
				}
				for i := range a {
					if a[i] != b[i] {
						t.Fatalf("cell %d is %+v, want %+v, in %q", i, b[i], a[i], l)
					}
				}
			}
		}
	}
	canvas = nil
}

func TestARMCoreNames(t *testing.T) {
	cases := map[[2]string]string{
		{"0x41", "0xd08"}: "Cortex-A72",
		{"0x41", "0xD0B"}: "Cortex-A76",
		{"0x41", "0xfff"}: "ARM 0xfff",
		{"0x51", "0x804"}: "Qualcomm 0x804",
		{"", "0x001"}:     "ARM 0x001",
	}
	for in, want := range cases {
		if got := armCore(in[0], in[1]); got != want {
			t.Errorf("armCore(%q, %q) = %q; want %q", in[0], in[1], got, want)
		}
	}
}

// Android hides /proc/stat from apps. The cpu box must say so instead of
// showing an idle processor.
func TestCPUBoxWithoutUsage(t *testing.T) {
	depth = depthNone
	defer func() { depth = depthTrue }()
	in := fakeInfo()
	in.CPU.NoUsage, in.CPU.Total, in.CPU.PerCore = true, 0, nil
	lay := layout{hist: &history{cpu: []float64{0}}, d: detail{cpuH: 3}}
	out := strings.Join(cpuBox(builtinTheme(), in, 50, lay).lines, "\n")
	if !strings.Contains(out, "not available") || strings.Contains(out, "total") || !strings.Contains(out, "load") {
		t.Errorf("cpu box without usage:\n%s", out)
	}
}

func TestTruncKeepsEscapeCodesWhole(t *testing.T) {
	styled := "Hyprland 0.56.2\x1b[38;2;92;98;117m · wayland"
	for w := 1; w <= 30; w++ {
		got := trunc(styled, w)
		if strings.ContainsRune(wellFormed.ReplaceAllString(got, ""), 0x1b) {
			t.Fatalf("trunc(%d) = %q has a broken escape code", w, got)
		}
		if vlen(got) > w {
			t.Fatalf("trunc(%d) = %q is %d cells", w, got, vlen(got))
		}
	}
}
