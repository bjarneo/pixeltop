package main

import (
	"fmt"
	"math"
	"strings"
)

// ── visible width ───────────────────────────────────────────────────────────

// vlen returns the number of terminal cells that s fills, without escapes.
func vlen(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i = skipEscape(s, i)
			continue
		}
		r, size := decodeRune(s[i:])
		n += runeWidth(r)
		i += size
	}
	return n
}

func skipEscape(s string, i int) int {
	if i+1 >= len(s) {
		return len(s)
	}
	switch s[i+1] {
	case '[':
		// Parameter and intermediate bytes run from 0x20 to 0x3f. Any other
		// byte ends the sequence, so a broken code never hides the text after
		// it from vlen.
		j := i + 2
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3f {
			j++
		}
		if j < len(s) && s[j] >= 0x40 && s[j] <= 0x7e {
			return j + 1
		}
		return j
	case ']':
		for j := i + 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
		}
		return len(s)
	}
	return i + 2
}

func decodeRune(s string) (rune, int) {
	for i, r := range s {
		if i == 0 {
			return r, len(string(r))
		}
	}
	return 0, 1
}

// runeWidth treats East Asian wide characters and emoji as two cells.
func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r >= 0x1100 && r <= 0x115f, r >= 0x2e80 && r <= 0xa4cf, r >= 0xac00 && r <= 0xd7a3,
		r >= 0xf900 && r <= 0xfaff, r >= 0xfe30 && r <= 0xfe4f, r >= 0xff00 && r <= 0xff60,
		r >= 0xffe0 && r <= 0xffe6, r >= 0x1f300 && r <= 0x1f64f, r >= 0x1f900 && r <= 0x1f9ff,
		r >= 0x20000 && r <= 0x3fffd:
		return 2
	}
	return 1
}

// trunc shortens text to w cells and marks the cut with an ellipsis. It
// keeps escape codes whole, so styled text is safe to cut.
func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if vlen(s) <= w {
		return s
	}
	return clip(s, w-1) + "…"
}

// pad fills styled text with spaces up to w cells.
func pad(s string, w int) string {
	if n := vlen(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func padLeft(s string, w int) string {
	if n := vlen(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
}

// spread puts left and right text at the two ends of a w-cell line. When
// both do not fit, the right text goes.
func spread(left, right string, w int) string {
	gap := w - vlen(left) - vlen(right)
	if gap < 1 {
		return clip(left, w)
	}
	return left + strings.Repeat(" ", gap) + right
}

// clip cuts styled text to w visible cells and keeps the escape codes.
func clip(s string, w int) string {
	if vlen(s) <= w {
		return s
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := skipEscape(s, i)
			b.WriteString(s[i:j])
			i = j
			continue
		}
		r, size := decodeRune(s[i:])
		if n+runeWidth(r) > w {
			break
		}
		b.WriteString(s[i : i+size])
		n += runeWidth(r)
		i += size
	}
	return b.String()
}

// ── boxes ───────────────────────────────────────────────────────────────────

var superscript = []string{"⁰", "¹", "²", "³", "⁴", "⁵", "⁶", "⁷", "⁸", "⁹"}

type box struct {
	title string
	num   int
	color RGB
	right string // label in the top border, at the right
	foot  string // label in the bottom border, at the right
	lines []string
}

// inner is the content width of a box that is w cells wide.
func inner(w int) int { return w - 4 }

// render draws the box with rounded corners and a btop style title.
// Content lines must already fit the inner width.
func (b *box) render(t *Theme, w, h int) []string {
	// Every line starts with rst, so that a canvas also paints the border.
	line := fg(b.color)
	edge := func(s string) string { return line + s }
	out := make([]string, 0, h)

	title := ""
	tw := 0
	if b.title != "" {
		sup := ""
		if b.num > 0 && b.num < len(superscript) {
			sup = superscript[b.num]
		}
		title = edge("┐") + fg(t.Accent) + bold(sup) + fg(t.Foreground) + bold(b.title) + edge("┌")
		tw = vlen(sup) + vlen(b.title) + 2
	}
	right := ""
	rw := 0
	if b.right != "" && w-5-tw-vlen(b.right) > 3 {
		right = edge("┐") + fg(t.Foreground) + b.right + edge("┌─")
		rw = vlen(b.right) + 3
	}
	fill := max(0, w-3-tw-rw)
	out = append(out, rst()+edge("╭─")+title+edge(strings.Repeat("─", fill))+right+edge("╮"))

	for i := range h - 2 {
		content := ""
		if i < len(b.lines) {
			content = b.lines[i]
		}
		out = append(out, rst()+edge("│")+rst()+" "+pad(clip(content, inner(w)), inner(w))+rst()+" "+edge("│"))
	}

	foot := ""
	fw := 0
	if b.foot != "" && w-6-vlen(b.foot) > 2 {
		foot = edge("┘") + fg(t.dim()) + b.foot + edge("└─")
		fw = vlen(b.foot) + 3
	}
	out = append(out, rst()+edge("╰"+strings.Repeat("─", max(0, w-2-fw)))+foot+edge("╯")+rst())
	return out
}

// height is the natural height of the box: its lines plus two borders.
func (b *box) height() int { return len(b.lines) + 2 }

// column stacks boxes in one column. The last box grows to fill h.
func column(t *Theme, boxes []*box, w, h int) []string {
	var out []string
	used := 0
	for _, b := range boxes {
		used += b.height()
	}
	for i, b := range boxes {
		bh := b.height()
		if i == len(boxes)-1 && h > used {
			bh += h - used
		}
		out = append(out, b.render(t, w, bh)...)
	}
	return out
}

func stackHeight(boxes []*box) int {
	n := 0
	for _, b := range boxes {
		n += b.height()
	}
	return n
}

// hjoin places blocks of lines side by side. Short blocks get blank lines.
func hjoin(widths []int, blocks ...[]string) []string {
	rows := 0
	for _, b := range blocks {
		rows = max(rows, len(b))
	}
	out := make([]string, rows)
	for r := range rows {
		var sb strings.Builder
		for i, b := range blocks {
			if r < len(b) {
				sb.WriteString(pad(b[r], widths[i]))
			} else {
				sb.WriteString(strings.Repeat(" ", widths[i]))
			}
			sb.WriteString(rst())
		}
		out[r] = sb.String()
	}
	return out
}

// ── widgets ─────────────────────────────────────────────────────────────────

// meter draws a btop style bar of w squares. The gradient spans the whole
// bar, so a low value only shows the first colors.
func meter(t *Theme, w int, frac float64, stops []RGB) string {
	if w <= 0 {
		return ""
	}
	frac = clamp01(frac)
	filled := int(math.Round(frac * float64(w)))
	if frac > 0 && filled == 0 {
		filled = 1
	}
	var b strings.Builder
	for i := range w {
		if i < filled {
			pos := 0.0
			if w > 1 {
				pos = float64(i) / float64(w-1)
			}
			b.WriteString(fg(gradient(stops, pos)))
		} else if i == filled {
			b.WriteString(fg(t.track()))
		}
		if i >= filled && depth == depthNone {
			b.WriteString("·")
			continue
		}
		b.WriteString("■")
	}
	return b.String()
}

var bars = []rune(" ▁▂▃▄▅▆▇█")

// sparkbars draws one vertical bar per value, colored by its height.
func sparkbars(values []float64, stops []RGB, gap bool) string {
	var b strings.Builder
	for i, v := range values {
		if gap && i > 0 {
			b.WriteByte(' ')
		}
		idx := int(math.Round(clamp01(v) * 8))
		if idx == 0 {
			idx = 1
		}
		b.WriteString(fg(gradient(stops, v)))
		b.WriteRune(bars[idx])
	}
	return b.String()
}

// kv formats one "label  value" line.
func kv(t *Theme, label string, lw int, value string) string {
	return fg(t.dim()) + pad(label, lw) + fg(t.Foreground) + value
}

// ── formatting ──────────────────────────────────────────────────────────────

func human(b uint64) string {
	const unit = 1024
	v := float64(b)
	suffix := []string{"B", "K", "M", "G", "T", "P"}
	i := 0
	for v >= unit && i < len(suffix)-1 {
		v /= unit
		i++
	}
	if v >= 100 || i == 0 {
		return fmt.Sprintf("%.0f%s", v, suffix[i])
	}
	return fmt.Sprintf("%.1f%s", v, suffix[i])
}

func duration(d interface{ Minutes() float64 }) string {
	m := int(d.Minutes())
	days, hours, mins := m/1440, m/60%24, m%60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func pct(frac float64) string { return fmt.Sprintf("%3.0f%%", clamp01(frac)*100) }

// compactSGR removes color and bold codes that repeat the current state.
// The renderer sets colors often, and the live view sends every frame, so
// this cuts the bytes that go to a slow terminal or over SSH.
func compactSGR(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var curFG, curBG string
	bolded, dirty := false, true
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			j := strings.IndexByte(s[i:], 0x1b)
			if j < 0 {
				j = len(s) - i
			}
			b.WriteString(s[i : i+j])
			i += j
			continue
		}
		j := skipEscape(s, i)
		seq := s[i:j]
		i = j
		if len(seq) < 3 || seq[1] != '[' || seq[len(seq)-1] != 'm' {
			b.WriteString(seq)
			continue
		}
		switch p := seq[2 : len(seq)-1]; {
		case p == "0" || p == "":
			if dirty || curFG != "" || curBG != "" || bolded {
				b.WriteString(seq)
			}
			curFG, curBG, bolded, dirty = "", "", false, false
		case strings.HasPrefix(p, "38;"):
			if p != curFG {
				b.WriteString(seq)
				curFG = p
			}
		case strings.HasPrefix(p, "48;"):
			if p != curBG {
				b.WriteString(seq)
				curBG = p
			}
		case p == "1":
			if !bolded {
				b.WriteString(seq)
				bolded = true
			}
		case p == "22":
			if bolded {
				b.WriteString(seq)
				bolded = false
			}
		default:
			b.WriteString(seq)
			dirty = true
		}
	}
	return b.String()
}
