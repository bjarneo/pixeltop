package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// history holds samples for the live view.
type history struct {
	cpu    []float64
	rx, tx []float64 // bytes per second
}

// detail sets how much each box shows. The zero value shows everything, and
// a larger value shows less. The layout engine raises these values until the
// dashboard fits the terminal.
type detail struct {
	logo  int // 0 full size, 1 small, 2 hidden
	theme int // 0 all rows, 1 no desktop rows, 2 name and palette only
	sys   int // 0 all rows, 1 compact, 2 minimal, 3 hidden
	cpu   int // 0 all rows, 1 no load row, 2 total and cores only
	cpuH  int // rows of the cpu graph in the live view
	mem   int // 0 all rows, 1 ram, swap and one disk, 2 ram only
	net   int // 0 all rows, 1 ip and totals, 2 hidden
	netH  int // rows of each network graph in the live view
	bat   int // 0 all rows, 1 meter only, 2 hidden
}

// layout carries the live data and the labels that go into the borders.
type layout struct {
	hist  *history // nil in the snapshot view
	clock string   // label in the sys box footer
	hint  string   // label in the theme box footer
	stack bool     // one box per row, for the one column plan
	d     detail
}

// minWidth and minHeight are the smallest terminal that the dashboard uses.
const (
	minWidth  = 24
	minHeight = 6
)

// ── layout engine ───────────────────────────────────────────────────────────

// plan is one arrangement of the boxes in columns.
type plan struct {
	widths  []int
	cols    [][]string
	penalty int  // makes a plan with more columns lose a tie
	stack   bool // one box per row
}

// Width breakpoints. Below twoColumns, the dashboard collapses to one
// column with one box per row. Each column of the other plans gets at least
// 38 cells. From shortTwoColumns, two columns are a fallback for a short
// terminal, such as a phone in landscape, where one column must drop boxes.
const (
	shortTwoColumns = 90
	twoColumns      = 100
	threeColumns    = 140
	fourColumns     = 200

	// shortTwoPenalty is the content that one column must lose, about two
	// whole boxes, before the fallback wins.
	shortTwoPenalty = 12
)

// plans lists the arrangements that fit the width, from the most preferred.
// More columns suit wide and short windows.
func plans(w int) []plan {
	var out []plan
	if w < twoColumns {
		out = append(out, plan{widths: []int{w}, cols: [][]string{{"theme", "sys", "cpu", "mem", "net", "bat"}}, stack: true})
	}
	if w < shortTwoColumns {
		return out
	}
	two := min(w, 128)
	lw := max(38, min(46, two*2/5))
	penalty := 0
	if w < twoColumns {
		penalty = shortTwoPenalty
	}
	out = append(out, plan{widths: []int{lw, two - lw}, penalty: penalty, cols: [][]string{{"theme"}, {"sys", "cpu", "mem", "net", "bat"}}})
	if w >= threeColumns {
		three := min(w, 180)
		lw := max(38, min(46, three*3/10))
		mw := (three - lw) / 2
		out = append(out, plan{widths: []int{lw, mw, three - lw - mw}, penalty: 1,
			cols: [][]string{{"theme"}, {"sys", "mem"}, {"cpu", "net", "bat"}}})
	}
	if w >= fourColumns {
		four := min(w, 240)
		lw := max(38, min(46, four/4))
		cw := (four - lw) / 3
		out = append(out, plan{widths: []int{lw, cw, cw, four - lw - 2*cw}, penalty: 2,
			cols: [][]string{{"theme"}, {"sys"}, {"cpu"}, {"mem", "net", "bat"}}})
	}
	return out
}

// reductions lists the ways to save rows, in the order to try them. The cost
// tells how much content a step removes, so that the engine can compare
// plans.
var reductions = []struct {
	cost  int
	apply func(*detail)
}{
	{1, func(d *detail) { d.netH = min(d.netH, 1) }},
	{1, func(d *detail) { d.cpuH = min(d.cpuH, 3) }},
	{1, func(d *detail) { d.cpuH = min(d.cpuH, 2) }},
	{2, func(d *detail) { d.logo = max(d.logo, 1) }},
	{2, func(d *detail) { d.sys = max(d.sys, 1) }},
	{2, func(d *detail) { d.netH = 0 }},
	{1, func(d *detail) { d.cpuH = min(d.cpuH, 1) }},
	{2, func(d *detail) { d.bat = max(d.bat, 1) }},
	{3, func(d *detail) { d.logo = 2 }},
	{3, func(d *detail) { d.mem = max(d.mem, 1) }},
	{2, func(d *detail) { d.cpuH = 0 }},
	{4, func(d *detail) { d.sys = max(d.sys, 2) }},
	{4, func(d *detail) { d.net = max(d.net, 1) }},
	{4, func(d *detail) { d.theme = max(d.theme, 1) }},
	{6, func(d *detail) { d.bat = 2 }},
	{5, func(d *detail) { d.cpu = max(d.cpu, 1) }},
	{5, func(d *detail) { d.mem = 2 }},
	{8, func(d *detail) { d.net = 2 }},
	{8, func(d *detail) { d.sys = 3 }},
	{8, func(d *detail) { d.theme = 2 }},
	{8, func(d *detail) { d.cpu = 2 }},
}

// dashboard renders the main view in w columns and at most rows rows. A
// rows value of 0 sets no height limit. It tries each plan and keeps the one
// that removes the least content.
func dashboard(t *Theme, in *Info, w, rows int, lay layout) []string {
	if w < minWidth || (rows > 0 && rows < minHeight) {
		return tooSmall(t, w, rows)
	}
	var best []string
	bestLoss := -1
	for _, p := range plans(w) {
		lines, loss := p.fit(t, in, rows, lay)
		if loss += p.penalty; bestLoss < 0 || loss < bestLoss {
			best, bestLoss = lines, loss
		}
	}
	return best
}

// fit reduces each column on its own until it fits the rows, then draws all
// columns at the same height.
func (p plan) fit(t *Theme, in *Info, rows int, lay layout) ([]string, int) {
	lay.stack = p.stack
	loss := 0
	lays := make([]layout, len(p.cols))
	heights := make([]int, len(p.cols))
	cols := slices.Clone(p.cols)
	for i, names := range cols {
		height := func(l layout) int { return columnHeight(t, in, names, p.widths[i], l) }
		replay := func(steps []int) layout {
			l := lay
			for _, k := range steps {
				reductions[k].apply(&l.d)
			}
			return l
		}

		l, h := lay, height(lay)
		var applied []int
		// Apply the first step that saves rows, then scan again from the
		// start. A step can start to save rows only after a later step, for
		// example a smaller logo beside a shorter list.
		for rows > 0 && h > rows {
			saved := false
			for k, r := range reductions {
				if slices.Contains(applied, k) {
					continue
				}
				next := l
				r.apply(&next.d)
				if nh := height(next); nh < h {
					l, h, saved = next, nh, true
					applied = append(applied, k)
					break
				}
			}
			if !saved {
				break
			}
		}
		// A large step can save more rows than needed. Undo each step that
		// the column can do without, the most severe first.
		if rows > 0 && h <= rows {
			for j := len(applied) - 1; j >= 0; j-- {
				fewer := slices.Delete(slices.Clone(applied), j, j+1)
				if fl := replay(fewer); height(fl) <= rows {
					applied, l, h = fewer, fl, height(fl)
				}
			}
		}
		for _, k := range applied {
			loss += reductions[k].cost
		}
		// As a last resort, drop whole boxes from the end of the column, so
		// that no box is cut in half.
		for rows > 0 && h > rows && len(names) > 1 {
			names = names[:len(names)-1]
			cols[i] = names
			h = columnHeight(t, in, names, p.widths[i], l)
			loss += 50
		}
		if rows > 0 && h > rows {
			loss += 100 * (h - rows)
		}
		lays[i], heights[i] = l, h
	}

	height := slices.Max(heights)
	if rows > 0 {
		height = min(height, rows)
	}
	blocks := make([][]string, len(cols))
	for i, names := range cols {
		blocks[i] = renderColumn(t, in, names, p.widths[i], lays[i], height)
	}
	lines := hjoin(p.widths, blocks...)
	if rows > 0 && len(lines) > rows {
		lines = lines[:rows]
	}
	return lines, loss
}

// unit is one row of a column: a box, or boxes side by side.
type unit struct {
	boxes  []*box
	widths []int
	grow   bool // takes spare rows when the column stretches
}

func (u unit) height() int {
	h := 0
	for _, b := range u.boxes {
		h = max(h, b.height())
	}
	return h
}

func (u unit) render(t *Theme, h int) []string {
	if len(u.boxes) == 1 {
		return u.boxes[0].render(t, u.widths[0], h)
	}
	blocks := make([][]string, len(u.boxes))
	for i, b := range u.boxes {
		blocks[i] = b.render(t, u.widths[i], h)
	}
	return hjoin(u.widths, blocks...)
}

// buildColumn makes the boxes of one column. Network and battery share a
// row when the column is wide enough.
func buildColumn(t *Theme, in *Info, names []string, w int, lay layout) []unit {
	var us []unit
	one := func(b *box, grow bool) { us = append(us, unit{boxes: []*box{b}, widths: []int{w}, grow: grow}) }
	showBat := in.Bat != nil && lay.d.bat < 2
	for i := 0; i < len(names); i++ {
		switch names[i] {
		case "theme":
			one(themeBox(t, in, w, lay), true)
		case "sys":
			if lay.d.sys < 3 {
				b := sysBox(t, in, w, lay.d.sys)
				b.foot = lay.clock
				one(b, false)
			}
		case "cpu":
			one(cpuBox(t, in, w, lay), false)
		case "mem":
			one(memBox(t, in, w, lay.d.mem), true)
		case "net":
			if lay.d.net >= 2 {
				continue
			}
			if i+1 < len(names) && names[i+1] == "bat" && showBat && !lay.stack && w >= 56 {
				nw := w * 11 / 20
				us = append(us, unit{boxes: []*box{netBox(t, in, nw, lay), batBox(t, in, w-nw, lay.d.bat)}, widths: []int{nw, w - nw}})
				i++
				continue
			}
			one(netBox(t, in, w, lay), false)
		case "bat":
			if showBat {
				one(batBox(t, in, w, lay.d.bat), false)
			}
		}
	}
	return us
}

func columnHeight(t *Theme, in *Info, names []string, w int, lay layout) int {
	h := 0
	for _, u := range buildColumn(t, in, names, w, lay) {
		h += u.height()
	}
	return h
}

// renderColumn draws a column at height h. In the live view the cpu graph
// takes the spare rows. Otherwise the last box that can grow takes them.
func renderColumn(t *Theme, in *Info, names []string, w int, lay layout, h int) []string {
	us := buildColumn(t, in, names, w, lay)
	natural := 0
	for _, u := range us {
		natural += u.height()
	}
	if spare := h - natural; spare > 0 && lay.hist != nil && slices.Contains(names, "cpu") {
		lay.d.cpuH += spare
		us = buildColumn(t, in, names, w, lay)
		natural = h
	}
	grow := -1
	for i, u := range us {
		if u.grow {
			grow = i
		}
	}
	var out []string
	for i, u := range us {
		uh := u.height()
		if i == grow && h > natural {
			uh += h - natural
		}
		out = append(out, u.render(t, uh)...)
	}
	return out
}

// tooSmall is the message for a terminal below the minimum size.
func tooSmall(t *Theme, w, rows int) []string {
	msg := []string{fg(t.Accent) + bold("pixeltop")}
	for _, l := range wrapWords("Make the terminal larger.", max(1, w)) {
		msg = append(msg, fg(t.Foreground)+l)
	}
	for _, l := range wrapWords(fmt.Sprintf("Needed %dx%d. Now %dx%d.", minWidth, minHeight, w, rows), max(1, w)) {
		msg = append(msg, fg(t.dim())+l)
	}
	var out []string
	top := max(0, (rows-len(msg))/2)
	if rows > 0 && len(msg) > rows {
		msg = msg[:rows]
	}
	for range top {
		out = append(out, "")
	}
	for _, m := range msg {
		out = append(out, strings.Repeat(" ", max(0, (w-vlen(m))/2))+clip(m, max(1, w)))
	}
	return out
}

// ── theme ───────────────────────────────────────────────────────────────────

func themeBox(t *Theme, in *Info, w int, lay layout) *box {
	iw := inner(w)
	b := &box{title: "theme", num: 1, color: t.Accent, right: t.Mode, foot: lay.hint}
	info := themeInfo(t, in, iw, lay.d.theme)
	if lay.d.logo >= 2 {
		b.lines = info
		return b
	}
	maxRows := 0
	if lay.d.logo == 1 {
		maxRows = 7
	}

	// A wide single column puts the logo beside the details, when the
	// details still get 40 cells. Otherwise the logo goes on top.
	if iw >= 70 {
		u := logoScale(iw*2/5, maxRows)
		logoW := 13*u + 2
		infoW := iw - logoW - 2
		b.lines = hjoin([]int{logoW, 2, infoW}, logoLines(t.logo(), logoW, u), nil, themeInfo(t, in, infoW, lay.d.theme))
		return b
	}
	b.lines = logoLines(t.logo(), iw, logoScale(iw, maxRows))
	b.lines = append(b.lines, "")
	b.lines = append(b.lines, info...)
	return b
}

// themeInfo lists the name, palette, key colors and desktop settings. A
// higher level shows fewer rows.
func themeInfo(t *Theme, in *Info, iw, level int) []string {
	var lines []string
	// The name wins over the source label when space is short.
	source := t.Source
	if t.Current && t.Source == "omarchy" {
		source = "omarchy · active"
		if vlen(t.Name)+vlen(source)+2 > iw {
			source = "active"
		}
	}
	if vlen(t.Name)+vlen(source)+2 > iw {
		source = ""
	}
	lines = append(lines, spread(fg(t.Accent)+bold(trunc(t.Name, iw)), fg(t.dim())+source, iw))
	if depth != depthNone {
		lines = append(lines, paletteStrip(t, iw)...)
	}
	if level >= 2 {
		return lines
	}
	lines = append(lines, "")

	// Four color chips in two rows. A narrow box uses short names to keep
	// two chips in each row.
	names, nameW := []string{"accent", "bg", "fg", "select"}, 7
	if iw/2 < 17 {
		names, nameW = []string{"acc", "bg", "fg", "sel"}, 4
	}
	colors := []RGB{t.Accent, t.Background, t.Foreground, t.Selection}
	var chips []string
	for i, n := range names {
		chips = append(chips, chip(t, n, colors[i], nameW))
	}
	if half := iw / 2; nameW == 7 || half > vlen(chips[0]) {
		lines = append(lines, pad(chips[0], half)+chips[1], pad(chips[2], half)+chips[3])
	} else {
		lines = append(lines, chips...)
	}
	ratio := contrast(t.Foreground, t.Background)
	label := fmt.Sprintf(" %4.1f %s", ratio, wcagGrade(ratio))
	bar := ""
	if mw := iw - 9 - vlen(label); mw >= 4 {
		bar = meter(t, mw, contrastFrac(ratio), []RGB{t.Red, t.Yellow, t.Green})
	}
	lines = append(lines, fg(t.dim())+"contrast "+bar+fg(t.Foreground)+label)
	if level >= 1 {
		return lines
	}
	lines = append(lines, "")

	lw := 7
	row := func(label, value string) {
		if value != "" {
			lines = append(lines, kv(t, label, lw, trunc(value, iw-lw)))
		}
	}
	if !t.Preview {
		row("gtk", in.Desk.GTK)
		row("icons", in.Desk.Icons)
		row("cursor", in.Desk.Cursor)
		row("font", in.Desk.Font)
		row("term", cmpOr(in.TermFont, in.Desk.Mono))
	} else if t.Dir != "" {
		x := t.extra()
		row("icons", x.icons)
		if x.walls > 1 {
			row("walls", fmt.Sprintf("%d images", x.walls))
		}
		if len(x.apps) > 0 {
			for i, l := range wrapWords(strings.Join(x.apps, " · "), iw-lw) {
				row(map[bool]string{true: "ships", false: ""}[i == 0], l)
			}
		}
	}
	if t.Wallpaper != "" {
		name := filepath.Base(t.Wallpaper)
		if x := t.extra(); x.wallW > 0 && iw-lw-len(name) > 10 {
			dims := fmt.Sprintf("%dx%d", x.wallW, x.wallH)
			row("wall", name+fg(t.dim())+" "+dims)
		} else {
			row("wall", name)
		}
	}
	return lines
}

// themeExtra holds theme facts that need file access. The dashboard draws
// many times while it fits the terminal, so these are read once per theme.
type themeExtra struct {
	wallW, wallH int
	icons        string
	walls        int
	apps         []string
}

func (t *Theme) extra() *themeExtra {
	if t.ex != nil {
		return t.ex
	}
	x := &themeExtra{}
	if t.Wallpaper != "" {
		x.wallW, x.wallH = imageSize(t.Wallpaper)
	}
	if t.Dir != "" {
		x.icons = readTrim(filepath.Join(t.Dir, "icons.theme"))
		x.walls = countImages(filepath.Join(t.Dir, "backgrounds"))
		x.apps = themeApps(t.Dir)
	}
	t.ex = x
	return x
}

// paletteStrip draws the eight colors as pixel squares: the normal color on
// the top row and the bright color on the bottom row.
func paletteStrip(t *Theme, iw int) []string {
	sw := swatches(t)
	n := len(sw)
	cell := max(1, min(4, (iw+1)/n-1))
	used := n*cell + n - 1
	lead := strings.Repeat(" ", max(0, (iw-used)/2))
	var top, bot strings.Builder
	top.WriteString(lead)
	bot.WriteString(lead)
	for i, s := range sw {
		if i > 0 {
			top.WriteString(" ")
			bot.WriteString(" ")
		}
		top.WriteString(fg(s.Normal) + strings.Repeat("█", cell))
		bot.WriteString(fg(s.Bright) + strings.Repeat("█", cell))
	}
	return []string{top.String(), bot.String()}
}

func swatches(t *Theme) []swatch { return t.swatches() }

// chip is a small color sample with its name and hex value. A color close
// to the background gets a frame, so that the sample stays visible.
func chip(t *Theme, name string, c RGB, nameW int) string {
	sample := fg(c) + "▐█▌"
	switch {
	case depth == depthNone:
		sample = ""
	case contrast(c, t.Background) < 1.25:
		sample = bg(t.track()) + sample + rst()
	}
	return sample + fg(t.dim()) + pad(name, nameW) + fg(t.Foreground) + c.Hex()
}

// ── system ──────────────────────────────────────────────────────────────────

// sysBox lists the system facts. Level 1 leaves out the slow-changing rows
// and level 2 keeps the first three rows.
func sysBox(t *Theme, in *Info, w, level int) *box {
	iw := inner(w)
	userHost := fg(t.Accent) + in.User + fg(t.dim()) + "@" + fg(t.Foreground) + in.Host
	b := &box{title: "sys", num: 2, color: t.Blue, right: userHost}
	// A narrow box uses short labels, so that the values keep their room.
	lw := 9
	short := map[string]string{}
	if iw < 36 {
		lw = 7
		short = map[string]string{"packages": "pkgs", "terminal": "term", "display": "disp"}
	}
	row := func(label, value string) {
		if value != "" {
			b.lines = append(b.lines, kv(t, cmpOr(short[label], label), lw, trunc(value, iw-lw)))
		}
	}
	osName := in.OS
	if in.OSLike != "" && !strings.Contains(strings.ToLower(osName), in.OSLike) {
		osName += " · " + in.OSLike
	}
	row("os", osName)
	if level < 2 {
		row("host", in.Model)
	}
	row("kernel", in.Kernel)
	row("uptime", duration(in.Uptime))
	if level >= 2 {
		return b
	}
	if level == 0 {
		row("packages", in.Packages)
		row("shell", in.Shell)
	}
	wm := in.WM
	if in.Session != "" && wm != "" {
		wm += fg(t.dim()) + " · " + in.Session
	}
	row("wm", wm)
	row("terminal", in.Terminal)
	if level >= 1 {
		return b
	}
	if len(in.Displays) > 0 {
		d := in.Displays[0]
		if len(in.Displays) > 1 {
			d += fmt.Sprintf(" +%d", len(in.Displays)-1)
		}
		row("display", d)
	}
	for i, g := range in.GPUs {
		if i == 2 {
			break
		}
		row(map[bool]string{true: "gpu", false: ""}[i == 0], g)
	}
	return b
}

func cpuBox(t *Theme, in *Info, w int, lay layout) *box {
	iw := inner(w)
	c := in.CPU
	level := lay.d.cpu
	right := ""
	if c.CurGHz > 0 {
		right = fmt.Sprintf("%.1f GHz", c.CurGHz)
	}
	b := &box{title: "cpu", num: 3, color: t.Magenta, right: right}

	if level < 2 {
		threads := fmt.Sprintf("%dc · %dt", c.Cores, c.Threads)
		if vlen(c.Model)+vlen(threads)+2 > iw {
			threads = ""
		}
		b.lines = append(b.lines, spread(fg(t.Foreground)+bold(trunc(c.Model, iw-vlen(threads)-2)), fg(t.dim())+threads, iw))
	}

	if lay.hist != nil && lay.d.cpuH > 0 && !c.NoUsage {
		b.lines = append(b.lines, graph(lay.hist.cpu, iw, lay.d.cpuH, t.heat(), t)...)
	}

	lw := 6
	temp := ""
	if c.Temp > 0 {
		temp = fg(gradient(t.heat(), (c.Temp-35)/60)) + fmt.Sprintf("%.0f°C", c.Temp)
	}
	if c.NoUsage {
		b.lines = append(b.lines, spread(kv(t, "usage", lw, fg(t.dim())+"not available"), temp, iw))
	} else {
		b.lines = append(b.lines, fg(t.dim())+pad("total", lw)+meter(t, iw-lw-5, c.Total, t.heat())+fg(t.Foreground)+" "+pct(c.Total))
	}

	cores := c.PerCore
	room := iw - lw - 1 - vlen(temp)
	gap := len(cores)*2-1 <= room
	if !gap && len(cores) > room {
		cores = squeeze(cores, max(0, room))
	}
	if !c.NoUsage {
		b.lines = append(b.lines, spread(fg(t.dim())+pad("cores", lw)+sparkbars(cores, t.heat(), gap), temp, iw))
	}

	if level < 1 {
		load := fmt.Sprintf("%.2f  %.2f  %.2f", c.Load[0], c.Load[1], c.Load[2])
		if iw < lw+len(load) {
			load = fmt.Sprintf("%.2f", c.Load[0])
		}
		b.lines = append(b.lines, spread(kv(t, "load", lw, load), fg(t.dim())+fmt.Sprintf("%d procs", c.Procs), iw))
	}
	return b
}

// squeeze averages values into n buckets.
func squeeze(v []float64, n int) []float64 {
	out := make([]float64, n)
	for i := range n {
		lo, hi := i*len(v)/n, (i+1)*len(v)/n
		sum := 0.0
		for _, x := range v[lo:max(hi, lo+1)] {
			sum += x
		}
		out[i] = sum / float64(max(1, hi-lo))
	}
	return out
}

// memBox shows memory, swap and disks. Level 1 keeps one disk and level 2
// keeps only the memory row. Narrow boxes leave out the sizes.
func memBox(t *Theme, in *Info, w, level int) *box {
	iw := inner(w)
	b := &box{title: "mem", num: 4, color: t.Green}
	m := in.Mem
	if m.Total > 0 {
		b.right = human(m.Total)
	}
	lw := 7
	showSize := iw-lw-5-13 >= 8
	bar := func(label string, used, total uint64) {
		if total == 0 {
			return
		}
		frac := float64(used) / float64(total)
		line := fg(t.dim()) + pad(trunc(label, lw-1), lw)
		if showSize {
			line += meter(t, iw-lw-5-13, frac, t.heat()) + fg(t.Foreground) + padLeft(human(used)+"/"+human(total), 13)
		} else {
			line += meter(t, iw-lw-5, frac, t.heat())
		}
		b.lines = append(b.lines, line+" "+fg(t.dim())+pct(frac))
	}
	bar("ram", m.Used, m.Total)
	if level >= 2 {
		return b
	}
	bar("swap", m.SwapUsed, m.SwapTotal)
	disks := 3
	if level >= 1 {
		disks = 1
	}
	for i, d := range in.Disks {
		if i == disks {
			break
		}
		bar(d.Mount, d.Used, d.Total)
	}
	return b
}

func netBox(t *Theme, in *Info, w int, lay layout) *box {
	iw := inner(w)
	n := in.Net
	b := &box{title: "net", num: 5, color: t.Red, right: n.Iface}
	if n.Iface == "" {
		b.lines = append(b.lines, fg(t.dim())+"offline")
		return b
	}
	lw := 6
	b.lines = append(b.lines, kv(t, "ip", lw, trunc(cmpOr(n.IP, "none"), iw-lw)))
	if h := lay.hist; h != nil && len(h.rx) > 0 && lay.d.netH > 0 && lay.d.net == 0 {
		last := func(v []float64) float64 { return v[len(v)-1] }
		b.lines = append(b.lines, spread(kv(t, "down", lw, fg(t.Cyan)+"↓ "+fg(t.Foreground)+rate(last(h.rx))), fg(t.dim())+human(n.RX), iw))
		b.lines = append(b.lines, graph(normalize(h.rx, 64<<10), iw, lay.d.netH, []RGB{t.Blue, t.Cyan}, t)...)
		b.lines = append(b.lines, spread(kv(t, "up", lw, fg(t.Orange)+"↑ "+fg(t.Foreground)+rate(last(h.tx))), fg(t.dim())+human(n.TX), iw))
		b.lines = append(b.lines, graph(normalize(h.tx, 64<<10), iw, lay.d.netH, []RGB{t.Yellow, t.Orange}, t)...)
	} else {
		b.lines = append(b.lines, kv(t, "total", lw, fg(t.Cyan)+"↓ "+fg(t.Foreground)+human(n.RX)+"  "+fg(t.Orange)+"↑ "+fg(t.Foreground)+human(n.TX)))
	}
	if n.Wifi && lay.d.net == 0 {
		q := clamp01(float64(n.Signal+90) / 60)
		label := fmt.Sprintf(" %d dBm", n.Signal)
		b.lines = append(b.lines, fg(t.dim())+pad("wifi", lw)+meter(t, iw-lw-vlen(label), q, []RGB{t.Red, t.Yellow, t.Green})+fg(t.Foreground)+label)
	}
	return b
}

// batBox shows the charge. Level 1 keeps only the meter.
func batBox(t *Theme, in *Info, w, level int) *box {
	iw := inner(w)
	bt := in.Bat
	b := &box{title: "bat", num: 6, color: t.Yellow, right: bt.Status}
	frac := float64(bt.Capacity) / 100
	p := fmt.Sprintf(" %d%%", bt.Capacity)
	b.lines = append(b.lines, meter(t, iw-vlen(p), frac, []RGB{t.Red, t.Yellow, t.Green})+fg(t.Foreground)+p)
	if level >= 1 {
		return b
	}
	var parts []string
	if bt.Watts > 0.1 {
		parts = append(parts, fmt.Sprintf("%.1f W", bt.Watts))
	}
	if bt.Remaining > 0 {
		verb := "left"
		if bt.Status == "charging" {
			verb = "to full"
		}
		parts = append(parts, duration(bt.Remaining)+" "+verb)
	}
	if len(parts) > 0 {
		b.lines = append(b.lines, fg(t.Foreground)+trunc(strings.Join(parts, " · "), iw))
	}
	if bt.Health > 0 {
		h := fmt.Sprintf("%.0f%%", bt.Health*100)
		if bt.Cycles > 0 {
			h += fmt.Sprintf(" · %d cycles", bt.Cycles)
		}
		b.lines = append(b.lines, kv(t, "health", 7, trunc(h, iw-7)))
	}
	return b
}

func rate(bps float64) string { return human(uint64(bps)) + "/s" }

// themeApps names the apps that a theme folder ships a config for.
func themeApps(dir string) []string {
	known := []struct{ file, app string }{
		{"alacritty.toml", "alacritty"}, {"btop.theme", "btop"}, {"chromium.theme", "chromium"},
		{"ghostty.conf", "ghostty"}, {"kitty.conf", "kitty"}, {"foot.ini", "foot"},
		{"helix.toml", "helix"}, {"neovim.lua", "neovim"}, {"vscode.json", "vscode"},
		{"vscode-theme.json", "vscode"}, {"obsidian.css", "obsidian"}, {"hyprland.conf", "hyprland"},
		{"hyprland.lua", "hyprland"}, {"hyprlock.conf", "hyprlock"}, {"waybar.css", "waybar"},
		{"mako.ini", "mako"}, {"walker.css", "walker"}, {"swayosd.css", "swayosd"},
		{"keyboard.rgb", "keyboard"}, {"shell.toml", "shell"}, {"claude.json", "claude"},
		{"gum_env.lua", "gum"}, {"wofi.css", "wofi"},
	}
	var out []string
	for _, k := range known {
		if _, err := os.Stat(filepath.Join(dir, k.file)); err == nil && !slices.Contains(out, k.app) {
			out = append(out, k.app)
		}
	}
	return out
}

func countImages(dir string) int {
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if isImage(e.Name()) {
			n++
		}
	}
	return n
}

// wrapWords breaks plain text into lines of at most w cells.
func wrapWords(s string, w int) []string {
	var out []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case vlen(line)+1+vlen(word) <= w:
			line += " " + word
		default:
			out = append(out, line)
			line = word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}
