package main

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Palette holds the semantic colors of a theme. The names follow the
// Omarchy colors.toml format.
type Palette struct {
	Background, DarkBackground, DarkerBackground, LighterBackground RGB
	Foreground, DarkForeground, LightForeground, BrightForeground   RGB
	Accent, Muted, Selection                                        RGB

	Red, Orange, Yellow, Green, Cyan, Blue, Magenta, Brown RGB

	BrightRed, BrightYellow, BrightGreen, BrightCyan, BrightBlue, BrightMagenta RGB
}

// Theme is a palette plus where it came from.
type Theme struct {
	Name      string
	Source    string // omarchy, file, terminal or builtin
	Dir       string
	Mode      string // dark or light
	Wallpaper string
	File      string // the color file that the palette comes from
	Current   bool   // the theme is active on this desktop
	Preview   bool   // the theme was picked with --theme and is not active
	Palette

	ex *themeExtra // file facts, read on first use
}

// swatch is one named palette entry, for views that list colors.
type swatch struct {
	Name   string
	Normal RGB
	Bright RGB
}

func (t *Theme) swatches() []swatch {
	p := t.Palette
	return []swatch{
		{"red", p.Red, p.BrightRed},
		{"orange", p.Orange, blend(p.Orange, p.BrightYellow, 0.35)},
		{"yellow", p.Yellow, p.BrightYellow},
		{"green", p.Green, p.BrightGreen},
		{"cyan", p.Cyan, p.BrightCyan},
		{"blue", p.Blue, p.BrightBlue},
		{"magenta", p.Magenta, p.BrightMagenta},
		{"brown", p.Brown, blend(p.Brown, p.Orange, 0.35)},
	}
}

// shades returns the background to foreground ramp of the theme.
func (t *Theme) shades() []swatch {
	p := t.Palette
	return []swatch{
		{"darker_background", p.DarkerBackground, p.DarkerBackground},
		{"dark_background", p.DarkBackground, p.DarkBackground},
		{"background", p.Background, p.Background},
		{"lighter_background", p.LighterBackground, p.LighterBackground},
		{"selection", p.Selection, p.Selection},
		{"muted", p.Muted, p.Muted},
		{"dark_foreground", p.DarkForeground, p.DarkForeground},
		{"foreground", p.Foreground, p.Foreground},
		{"light_foreground", p.LightForeground, p.LightForeground},
		{"bright_foreground", p.BrightForeground, p.BrightForeground},
	}
}

// ── derived roles used by the renderer ──────────────────────────────────────

// dim is the color for labels and secondary text. It keeps a readable
// contrast even when a theme sets muted close to the background.
func (t *Theme) dim() RGB {
	c := t.Muted
	if contrast(c, t.Background) < 2.6 {
		c = blend(t.Background, t.Foreground, 0.55)
	}
	return c
}

// track is the color of the empty part of a meter.
func (t *Theme) track() RGB { return blend(t.Background, t.Foreground, 0.16) }

// logo is the gradient of the Omarchy mark: from the accent to cyan.
func (t *Theme) logo() []RGB {
	first := t.Accent
	if dist(t.Accent, t.Blue) < 40*40 {
		first = t.Magenta
	}
	return []RGB{first, t.Blue, t.Cyan}
}

// heat is the low to high gradient for meters, as in btop.
func (t *Theme) heat() []RGB { return []RGB{t.Green, t.Yellow, t.Red} }

// ── loading ─────────────────────────────────────────────────────────────────

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func omarchyPath() string {
	if p := os.Getenv("OMARCHY_PATH"); p != "" {
		return p
	}
	return filepath.Join(home(), ".local/share/omarchy")
}

// themeRoots lists the folders that hold installed Omarchy themes.
func themeRoots() []string {
	return []string{
		filepath.Join(home(), ".config/omarchy/themes"),
		filepath.Join(omarchyPath(), "themes"),
	}
}

// currentThemeDir returns the folder of the active Omarchy theme.
func currentThemeDir() (dir, name, wallpaper string) {
	for _, state := range []string{
		filepath.Join(home(), ".local/state/omarchy/current"),
		filepath.Join(home(), ".config/omarchy/current"),
	} {
		d := filepath.Join(state, "theme")
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			continue
		}
		name = readTrim(filepath.Join(state, "theme.name"))
		if name == "" {
			if real, err := filepath.EvalSymlinks(d); err == nil {
				name = filepath.Base(real)
			}
		}
		if w, err := filepath.EvalSymlinks(filepath.Join(state, "background")); err == nil {
			wallpaper = w
		}
		return d, name, wallpaper
	}
	return "", "", ""
}

// loadTheme resolves a theme from a name, a folder or a file. An empty spec
// loads the active theme, then the terminal palette, then the built-in one.
func loadTheme(spec string, queryTerm bool) (*Theme, error) {
	curDir, curName, curWall := currentThemeDir()

	if spec == "" {
		if curDir != "" {
			if t, err := themeFromDir(curDir); err == nil {
				t.Name, t.Wallpaper, t.Current = curName, curWall, true
				return t, nil
			}
		}
		if queryTerm {
			if raw := queryTerminalPalette(); raw != nil {
				pal, mode := resolvePalette(raw, "")
				return &Theme{Name: "terminal", Source: "terminal", Mode: mode, Palette: pal}, nil
			}
		}
		return builtinTheme(), nil
	}

	if spec == "pixeltop" || spec == "builtin" {
		return builtinTheme(), nil
	}

	path := spec
	if !strings.ContainsRune(spec, '/') {
		path = ""
		for _, root := range themeRoots() {
			if st, err := os.Stat(filepath.Join(root, spec)); err == nil && st.IsDir() {
				path = filepath.Join(root, spec)
				break
			}
		}
		if path == "" {
			return nil, errors.New("theme not found: " + spec + ". Run `pixeltop themes` to list installed themes")
		}
	}

	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	var t *Theme
	if st.IsDir() {
		t, err = themeFromDir(path)
	} else {
		t, err = themeFromFile(path)
	}
	if err != nil {
		return nil, err
	}
	if curName != "" && t.Name == curName {
		t.Current, t.Wallpaper = true, curWall
	} else {
		t.Preview = true
	}
	return t, nil
}

// themeFromDir reads the best color source that a theme folder ships.
func themeFromDir(dir string) (*Theme, error) {
	for _, f := range []string{"colors.toml", "alacritty.toml", "kitty.conf", "ghostty.conf"} {
		p := filepath.Join(dir, f)
		if _, err := os.Stat(p); err == nil {
			t, err := themeFromFile(p)
			if err != nil {
				continue
			}
			t.Name = filepath.Base(dir)
			t.Dir = dir
			t.Source = "omarchy"
			t.Wallpaper = firstWallpaper(filepath.Join(dir, "backgrounds"))
			return t, nil
		}
	}
	return nil, errors.New("no colors.toml, alacritty.toml, kitty.conf or ghostty.conf in " + dir)
}

func themeFromFile(path string) (*Theme, error) {
	var raw map[string]string
	var err error
	switch base := filepath.Base(path); {
	case strings.HasPrefix(base, "alacritty"):
		raw, err = parseAlacritty(path)
	case strings.HasPrefix(base, "kitty"):
		raw, err = parseKitty(path)
	case strings.HasPrefix(base, "ghostty"):
		raw, err = parseGhostty(path)
	default:
		raw, err = parseColorsToml(path)
	}
	if err != nil {
		return nil, err
	}
	if raw["background"] == "" && raw["color0"] == "" && raw["bg"] == "" {
		return nil, errors.New("no background color in " + path)
	}
	dir := filepath.Dir(path)
	lightFile := ""
	if _, err := os.Stat(filepath.Join(dir, "light.mode")); err == nil {
		lightFile = "light"
	}
	pal, mode := resolvePalette(raw, lightFile)
	return &Theme{Name: filepath.Base(dir), Source: "file", Dir: dir, File: path, Mode: mode, Palette: pal}, nil
}

func firstWallpaper(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if isImage(e.Name()) {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

func isImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		return true
	}
	return false
}

// builtinTheme is the palette that pixeltop uses when nothing else is found.
func builtinTheme() *Theme {
	raw := map[string]string{
		"background": "#0f1117", "foreground": "#d5d8e6", "accent": "#8a9cff",
		"muted": "#5c6275", "selection": "#262a37", "lighter_background": "#1a1d27",
		"dark_foreground": "#9aa0b4",
		"red":             "#ff6b81", "orange": "#ff9f5a", "yellow": "#ffd166", "green": "#7bd88f",
		"cyan": "#5fd4d8", "blue": "#7aa2ff", "magenta": "#c792ea", "brown": "#a0785a",
	}
	pal, mode := resolvePalette(raw, "")
	return &Theme{Name: "pixeltop", Source: "builtin", Mode: mode, Palette: pal}
}

// ── colors.toml resolution ──────────────────────────────────────────────────

// resolvePalette fills missing keys the same way as omarchy-theme-color, so
// that pixeltop shows the colors that Omarchy renders into app configs.
func resolvePalette(in map[string]string, lightFile string) (Palette, string) {
	c := make(map[string]string, len(in)+32)
	for k, v := range in {
		c[k] = v
	}
	set := func(k string) bool { return c[k] != "" }
	alias := func(k, from string) {
		if !set(k) {
			c[k] = c[from]
		}
	}
	first := func(keys ...string) string {
		for _, k := range keys {
			if set(k) {
				return c[k]
			}
		}
		return ""
	}
	mixHex := func(a, b string, t float64) string {
		ca, ok1 := parseColor(a)
		cb, ok2 := parseColor(b)
		if !ok1 || !ok2 {
			return a
		}
		return mix(ca, cb, t).Hex()
	}

	for k, short := range map[string]string{
		"background": "bg", "dark_background": "dark_bg", "darker_background": "darker_bg",
		"lighter_background": "lighter_bg", "foreground": "fg", "dark_foreground": "dark_fg",
		"light_foreground": "light_fg", "bright_foreground": "bright_fg",
	} {
		alias(k, short)
	}
	alias("background", "color0")
	alias("foreground", "color7")
	if set("background") {
		c["color0"] = c["background"]
	}
	if set("foreground") {
		c["color7"] = c["foreground"]
	}
	for k, n := range map[string]string{
		"red": "color1", "green": "color2", "yellow": "color3", "blue": "color4",
		"magenta": "color5", "cyan": "color6", "bright_red": "color9", "bright_green": "color10",
		"bright_yellow": "color11", "bright_blue": "color12", "bright_magenta": "color13",
		"bright_cyan": "color14",
	} {
		alias(k, n)
	}
	alias("magenta", "purple")
	alias("bright_magenta", "bright_purple")

	if !set("light_foreground") {
		c["light_foreground"] = first("color7", "foreground")
	}
	if !set("bright_foreground") {
		c["bright_foreground"] = first("color15", "foreground")
	}
	if !set("lighter_background") {
		c["lighter_background"] = first("color0", "background")
	}
	if !set("dark_foreground") {
		c["dark_foreground"] = first("color8", "foreground")
	}
	if !set("muted") {
		c["muted"] = first("color8", "dark_foreground")
	}
	if !set("selection") {
		c["selection"] = first("selection_background", "color8", "color0", "background")
	}
	alias("orange", "yellow")
	if !set("brown") {
		c["brown"] = mixHex(c["orange"], "#000000", 0.5)
	}
	if !set("dark_background") {
		c["dark_background"] = mixHex(c["background"], "#000000", 0.25)
	}
	if !set("darker_background") {
		c["darker_background"] = mixHex(c["background"], "#000000", 0.5)
	}
	for _, k := range []string{"red", "yellow", "green", "cyan", "blue", "magenta"} {
		if !set("bright_" + k) {
			c["bright_"+k] = mixHex(c[k], "#ffffff", 0.2)
		}
	}
	alias("accent", "blue")

	get := func(k string, fallback RGB) RGB {
		if v, ok := parseColor(c[k]); ok {
			return v
		}
		return fallback
	}
	bgc := get("background", RGB{0x10, 0x10, 0x14})
	fgc := get("foreground", RGB{0xd0, 0xd0, 0xd8})
	p := Palette{
		Background:        bgc,
		DarkBackground:    get("dark_background", mix(bgc, RGB{}, 0.25)),
		DarkerBackground:  get("darker_background", mix(bgc, RGB{}, 0.5)),
		LighterBackground: get("lighter_background", bgc),
		Foreground:        fgc,
		DarkForeground:    get("dark_foreground", fgc),
		LightForeground:   get("light_foreground", fgc),
		BrightForeground:  get("bright_foreground", fgc),
		Muted:             get("muted", mix(bgc, fgc, 0.45)),
		Selection:         get("selection", mix(bgc, fgc, 0.2)),
		Red:               get("red", RGB{0xe0, 0x6c, 0x75}),
		Yellow:            get("yellow", RGB{0xe5, 0xc0, 0x7b}),
		Green:             get("green", RGB{0x98, 0xc3, 0x79}),
		Cyan:              get("cyan", RGB{0x56, 0xb6, 0xc2}),
		Blue:              get("blue", RGB{0x61, 0xaf, 0xef}),
		Magenta:           get("magenta", RGB{0xc6, 0x78, 0xdd}),
	}
	p.Orange = get("orange", p.Yellow)
	p.Brown = get("brown", mix(p.Orange, RGB{}, 0.5))
	p.Accent = get("accent", p.Blue)
	p.BrightRed = get("bright_red", mix(p.Red, RGB{255, 255, 255}, 0.2))
	p.BrightYellow = get("bright_yellow", mix(p.Yellow, RGB{255, 255, 255}, 0.2))
	p.BrightGreen = get("bright_green", mix(p.Green, RGB{255, 255, 255}, 0.2))
	p.BrightCyan = get("bright_cyan", mix(p.Cyan, RGB{255, 255, 255}, 0.2))
	p.BrightBlue = get("bright_blue", mix(p.Blue, RGB{255, 255, 255}, 0.2))
	p.BrightMagenta = get("bright_magenta", mix(p.Magenta, RGB{255, 255, 255}, 0.2))

	mode := first("mode", "theme_type")
	if mode == "" {
		mode = lightFile
	}
	if mode == "" {
		if int(bgc.R)+int(bgc.G)+int(bgc.B) > 382 {
			mode = "light"
		} else {
			mode = "dark"
		}
	}
	return p, strings.ToLower(mode)
}

// ── color file parsers ──────────────────────────────────────────────────────

// parseColorsToml reads key = "value" lines, as omarchy-theme-color does.
func parseColorsToml(path string) (map[string]string, error) {
	out := map[string]string{}
	err := eachLine(path, func(line string) {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return
		}
		key = strings.Trim(strings.TrimSpace(key), `"'`)
		if key == "" || strings.HasPrefix(key, "#") || strings.HasPrefix(key, "[") {
			return
		}
		out[key] = unquote(value)
	})
	return out, err
}

// parseAlacritty maps the [colors.*] tables of an alacritty.toml.
func parseAlacritty(path string) (map[string]string, error) {
	names := []string{"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"}
	out := map[string]string{}
	section := ""
	err := eachLine(path, func(line string) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[] ")
			return
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return
		}
		key, value = strings.TrimSpace(key), unquote(value)
		switch section {
		case "colors.primary":
			if key == "background" || key == "foreground" {
				out[key] = value
			}
		case "colors.normal", "colors.bright":
			if i := slices.Index(names, key); i >= 0 {
				if section == "colors.bright" {
					i += 8
				}
				out["color"+itoa(i)] = value
			}
		case "colors.selection":
			if key == "background" {
				out["selection_background"] = value
			}
		}
	})
	return out, err
}

// parseKitty reads "key value" color lines from a kitty.conf.
func parseKitty(path string) (map[string]string, error) {
	out := map[string]string{}
	err := eachLine(path, func(line string) {
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasPrefix(f[0], "#") {
			return
		}
		switch {
		case f[0] == "background", f[0] == "foreground", f[0] == "selection_background":
			out[f[0]] = f[1]
		case f[0] == "active_border_color":
			out["accent"] = f[1]
		case strings.HasPrefix(f[0], "color"):
			out[f[0]] = f[1]
		}
	})
	return out, err
}

// parseGhostty reads background, foreground and palette = N=#hex lines.
func parseGhostty(path string) (map[string]string, error) {
	out := map[string]string{}
	err := eachLine(path, func(line string) {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "background", "foreground", "selection-background":
			if !strings.HasPrefix(value, "#") {
				value = "#" + value
			}
			out[strings.ReplaceAll(key, "-", "_")] = value
		case "palette":
			if n, hex, ok := strings.Cut(value, "="); ok {
				out["color"+strings.TrimSpace(n)] = strings.TrimSpace(hex)
			}
		}
	})
	return out, err
}

func unquote(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.IndexAny(v, `"'`); i >= 0 {
		q := v[i]
		v = v[i+1:]
		if j := strings.IndexByte(v, q); j >= 0 {
			v = v[:j]
		}
		return v
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

func eachLine(path string, fn func(string)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fn(sc.Text())
	}
	return sc.Err()
}

// listThemes returns every installed theme, sorted by name. A user theme
// hides a stock theme with the same name.
func listThemes() []*Theme {
	seen := map[string]bool{}
	var out []*Theme
	for _, root := range themeRoots() {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if seen[e.Name()] || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			dir := filepath.Join(root, e.Name())
			if st, err := os.Stat(dir); err != nil || !st.IsDir() {
				continue
			}
			t, err := themeFromDir(dir)
			if err != nil {
				continue
			}
			seen[e.Name()] = true
			out = append(out, t)
		}
	}
	_, curName, curWall := currentThemeDir()
	for _, t := range out {
		if t.Name == curName {
			t.Current = true
			if curWall != "" {
				t.Wallpaper = curWall
			}
		}
	}
	slices.SortFunc(out, func(a, b *Theme) int { return strings.Compare(a.Name, b.Name) })
	return out
}
