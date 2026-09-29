package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

// ── palette view ────────────────────────────────────────────────────────────

// paletteView lists every color of a theme for theme authors: hex values,
// contrast against the background, and a preview of code and a terminal.
func paletteView(t *Theme, w int) []string {
	var out []string
	colors := &box{title: "colors", num: 1, color: t.Accent, right: t.Name}
	iw := inner(w)

	// A wide box shows one row per color. A narrow box collapses each color
	// onto two rows: the hex values, then the contrast.
	if iw >= 55 {
		colors.lines = append(colors.lines, fg(t.dim())+pad("", 10)+pad("name", 9)+pad("normal", 9)+pad("bright", 9)+"contrast")
		for _, s := range t.swatches() {
			ratio := contrast(s.Normal, t.Background)
			grade := fmt.Sprintf(" %4.1f %-4s", ratio, wcagGrade(ratio))
			line := fg(s.Normal) + "████" + fg(s.Bright) + "████  " +
				fg(t.Foreground) + pad(s.Name, 9) + fg(s.Normal) + pad(s.Normal.Hex(), 9) + fg(s.Bright) + pad(s.Bright.Hex(), 9) +
				meter(t, iw-37-len(grade), contrastFrac(ratio), []RGB{t.Red, t.Yellow, t.Green})
			colors.lines = append(colors.lines, line+fg(gradeColor(t, ratio))+grade)
		}
	} else {
		for _, s := range t.swatches() {
			ratio := contrast(s.Normal, t.Background)
			grade := fmt.Sprintf(" %4.1f %-4s", ratio, wcagGrade(ratio))
			top := fg(s.Normal) + "██" + fg(s.Bright) + "██ " + fg(t.Foreground) + pad(s.Name, 9) + fg(s.Normal) + s.Normal.Hex()
			bottom := pad("", 5)
			if iw >= 30 {
				top += " " + fg(s.Bright) + s.Bright.Hex()
			} else {
				bottom += fg(s.Bright) + s.Bright.Hex() + " "
			}
			if mw := iw - vlen(bottom) - len(grade); mw >= 4 {
				bottom += meter(t, mw, contrastFrac(ratio), []RGB{t.Red, t.Yellow, t.Green})
			}
			colors.lines = append(colors.lines, top, bottom+fg(gradeColor(t, ratio))+grade)
		}
	}
	out = append(out, colors.render(t, w, colors.height())...)

	shades := &box{title: "shades", num: 2, color: t.Blue, right: "ratio on background"}
	list := append([]swatch{{"accent", t.Accent, t.Accent}}, t.shades()...)
	perCol, cw := (len(list)+1)/2, iw/2
	if cw < 30 {
		perCol, cw = len(list), iw
	}
	nw := min(cw-19, 20)
	entry := func(s swatch) string {
		ratio := contrast(s.Normal, t.Background)
		return fg(s.Normal) + "███ " + fg(t.Foreground) + pad(trunc(s.Name, nw-1), nw) + fg(t.dim()) + s.Normal.Hex() + " " +
			fg(gradeColor(t, ratio)) + fmt.Sprintf("%5.1f", ratio)
	}
	for i := range perCol {
		l := pad(entry(list[i]), cw)
		if i+perCol < len(list) {
			l += entry(list[i+perCol])
		}
		shades.lines = append(shades.lines, l)
	}
	out = append(out, shades.render(t, w, shades.height())...)

	prev := &box{title: "preview", num: 3, color: t.Magenta, right: t.Mode}
	code := codePreview(t)
	term := termPreview(t)
	codeW := 50
	if iw-codeW-1 >= 30 {
		prev.lines = hjoin([]int{codeW, 1, iw - codeW - 1}, panel(t, code, codeW), nil, panel(t, term, iw-codeW-1))
	} else {
		prev.lines = append(panel(t, code, iw), panel(t, term, iw)...)
	}
	out = append(out, prev.render(t, w, prev.height())...)
	return out
}

func gradeColor(t *Theme, ratio float64) RGB {
	switch {
	case ratio >= 4.5:
		return t.Green
	case ratio >= 3:
		return t.Yellow
	}
	return t.Red
}

// panel paints lines on the theme background, padded to w cells.
func panel(t *Theme, lines []string, w int) []string {
	saved := canvas
	c := t.Background
	canvas = &c
	defer func() { canvas = saved }()
	out := make([]string, 0, len(lines)+2)
	blank := rst() + strings.Repeat(" ", w) + rst()
	out = append(out, blank)
	for _, l := range lines {
		out = append(out, rst()+" "+pad(clip(l, w-2), w-1)+rst())
	}
	return append(out, blank)
}

const codeSample = `package main

import "fmt"

// Pixel is one cell of the art.
type Pixel struct {
	X, Y  int
	Color string
}

func main() {
	p := Pixel{X: 4, Y: 2, Color: "#ff7edb"}
	if p.X > 0 && p.Y < 16 {
		fmt.Printf("at %d,%d\n", p.X, p.Y)
	}
}`

var (
	tokenRe  = regexp.MustCompile(`//.*|"(?:[^"\\]|\\.)*"|\b\d+\b|[A-Za-z_]\w*|\s+|.`)
	keywords = map[string]bool{"package": true, "import": true, "type": true, "struct": true, "func": true, "if": true, "return": true, "for": true, "range": true, "var": true}
	types    = map[string]bool{"int": true, "string": true, "bool": true, "float64": true, "error": true}
)

// codePreview highlights a small Go program with the theme colors.
func codePreview(t *Theme) []string {
	lines := strings.Split(codeSample, "\n")
	out := make([]string, 0, len(lines))
	for n, line := range lines {
		line = strings.ReplaceAll(line, "\t", "    ")
		var b strings.Builder
		b.WriteString(fg(t.dim()) + fmt.Sprintf("%2d  ", n+1))
		toks := tokenRe.FindAllStringIndex(line, -1)
		for i, span := range toks {
			tok := line[span[0]:span[1]]
			next := ""
			if i+1 < len(toks) {
				next = line[toks[i+1][0]:toks[i+1][1]]
			}
			c := t.Foreground
			switch {
			case strings.HasPrefix(tok, "//"):
				c = t.dim()
			case strings.HasPrefix(tok, `"`):
				c = t.Green
			case tok[0] >= '0' && tok[0] <= '9':
				c = t.Orange
			case keywords[tok]:
				c = t.Magenta
			case types[tok] || (tok[0] >= 'A' && tok[0] <= 'Z' && next != ":" && next != "("):
				c = t.Yellow
			case next == "(":
				c = t.Blue
			case next == ".":
				c = t.Cyan
			case strings.ContainsAny(tok, ":=&<>{}()!.,"):
				c = t.DarkForeground
			}
			b.WriteString(fg(c) + tok)
		}
		out = append(out, b.String())
	}
	return out
}

// termPreview shows the theme on typical terminal output.
func termPreview(t *Theme) []string {
	prompt := fg(t.Blue) + "~/pixel " + fg(t.Magenta) + "main " + fg(t.Green) + "❯ " + fg(t.Foreground)
	badge := func(c RGB, s string) string {
		return bg(c) + fg(t.Background) + bold(" "+s+" ") + rst()
	}
	return []string{
		prompt + "make test",
		badge(t.Green, "PASS") + fg(t.Foreground) + " render    " + fg(t.dim()) + "0.12s",
		badge(t.Green, "PASS") + fg(t.Foreground) + " palette   " + fg(t.dim()) + "0.08s",
		badge(t.Yellow, "WARN") + fg(t.Foreground) + " contrast  " + fg(t.Yellow) + "2 below AA",
		badge(t.Red, "FAIL") + fg(t.Foreground) + " gallery   " + fg(t.Red) + "webp decode",
		"",
		fg(t.Cyan) + "info " + fg(t.Foreground) + "cache at " + fg(t.dim()) + "~/.cache/pixeltop",
		fg(t.Orange) + "note " + fg(t.Foreground) + "3 files changed",
		"",
		bg(t.Selection) + fg(t.Foreground) + " selected text " + rst() + fg(t.Accent) + " accent" + fg(t.Foreground) + " link",
		"",
		prompt + bg(t.BrightForeground) + " " + rst(),
	}
}

// ── theme gallery ───────────────────────────────────────────────────────────

func gallery(filter string, width int) []string {
	themes := listThemes()
	if filter != "" {
		f := strings.ToLower(filter)
		themes = slicesFilter(themes, func(t *Theme) bool { return strings.Contains(strings.ToLower(t.Name), f) })
	}
	ui, _ := loadTheme("", false)

	if len(themes) == 0 {
		return []string{fg(ui.dim()) + "No themes match. Themes live in ~/.config/omarchy/themes and " + omarchyPath() + "/themes."}
	}

	cols := max(1, (width+1)/31)
	cw := min(40, (width+1)/cols-1)
	iw := inner(cw)
	artH := int(float64(iw)*9/16+0.5) &^ 1

	art := thumbnails(themes, iw, artH)

	var out []string
	active := ""
	for _, t := range themes {
		if t.Current {
			active = t.Name
		}
	}
	head := fg(ui.Accent) + bold("pixeltop themes") + fg(ui.dim()) + fmt.Sprintf("  %d installed", len(themes))
	if active != "" {
		head += fg(ui.dim()) + " · active " + fg(ui.Foreground) + active
	}
	out = append(out, clip(head, width), "")

	for i := 0; i < len(themes); i += cols {
		var blocks [][]string
		var widths []int
		for j := i; j < min(i+cols, len(themes)); j++ {
			if j > i {
				blocks = append(blocks, nil)
				widths = append(widths, 1)
			}
			blocks = append(blocks, card(themes[j], art[j], cw))
			widths = append(widths, cw)
		}
		out = append(out, hjoin(widths, blocks...)...)
	}
	out = append(out, "")
	for _, l := range wrapWords("Preview a theme with pixeltop --theme NAME or pixeltop palette --theme NAME", width) {
		out = append(out, fg(ui.dim())+l)
	}
	return out
}

// card draws one theme in its own colors on its own background.
func card(t *Theme, art *pixmap, w int) []string {
	saved := canvas
	c := t.Background
	canvas = &c
	defer func() { canvas = saved }()

	iw := inner(w)
	right := t.Mode
	if t.Current {
		right = "● active"
	}
	b := &box{title: trunc(t.Name, iw-vlen(right)-4), color: t.Accent, right: right}
	b.lines = append(b.lines, art.lines()...)

	sw := t.swatches()
	cell := iw / len(sw)
	lead := strings.Repeat(" ", (iw-cell*len(sw))/2)
	var strip strings.Builder
	strip.WriteString(lead)
	for _, s := range sw {
		strip.WriteString(fg(s.Normal) + bg(s.Bright) + strings.Repeat("▀", cell))
	}
	strip.WriteString(rst())
	b.lines = append(b.lines, strip.String())
	return b.render(t, w, b.height())
}

// thumbnails loads the art for every theme in parallel.
func thumbnails(themes []*Theme, w, h int) []*pixmap {
	out := make([]*pixmap, len(themes))
	jobs := make(chan int)
	var done atomic.Int32
	var wg sync.WaitGroup
	show := isTerminal(os.Stderr) && len(themes) > 4
	for range min(6, runtime.NumCPU()) {
		wg.Go(func() {
			for i := range jobs {
				t := themes[i]
				if t.Wallpaper != "" {
					out[i], _ = thumbnail(t.Wallpaper, w, h)
				}
				if out[i] == nil {
					out[i] = mosaic(t, w, h)
				}
				if n := done.Add(1); show {
					fmt.Fprintf(os.Stderr, "\r\x1b[2Kloading art %d/%d", n, len(themes))
				}
			}
		})
	}
	for i := range themes {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if show {
		fmt.Fprint(os.Stderr, "\r\x1b[2K")
	}
	return out
}

func slicesFilter[T any](in []T, keep func(T) bool) []T {
	var out []T
	for _, v := range in {
		if keep(v) {
			out = append(out, v)
		}
	}
	return out
}
