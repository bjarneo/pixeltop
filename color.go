package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// RGB is an 8-bit sRGB color.
type RGB struct{ R, G, B uint8 }

// parseColor reads #rgb, #rrggbb, #rrggbbaa, 0xrrggbb, rrggbb and rgb(r,g,b).
func parseColor(s string) (RGB, bool) {
	s = strings.TrimSpace(strings.Trim(s, `"'`))
	low := strings.ToLower(s)
	if strings.HasPrefix(low, "rgb") {
		open, end := strings.IndexByte(s, '('), strings.IndexByte(s, ')')
		if open < 0 || end < open {
			return RGB{}, false
		}
		inner := s[open+1 : end]
		// rgba(7dcfffee) is a Hyprland form: hex inside the parentheses.
		if !strings.Contains(inner, ",") {
			return parseColor("#" + inner)
		}
		parts := strings.Split(inner, ",")
		if len(parts) < 3 {
			return RGB{}, false
		}
		var v [3]uint8
		for i := range 3 {
			n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
			if err != nil {
				return RGB{}, false
			}
			v[i] = uint8(max(0, min(255, n)))
		}
		return RGB{v[0], v[1], v[2]}, true
	}
	switch {
	case strings.HasPrefix(low, "#"):
		s = s[1:]
	case strings.HasPrefix(low, "0x"):
		s = s[2:]
	}
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 && len(s) != 8 {
		return RGB{}, false
	}
	n, err := strconv.ParseUint(s[:6], 16, 32)
	if err != nil {
		return RGB{}, false
	}
	return RGB{uint8(n >> 16), uint8(n >> 8), uint8(n)}, true
}

func (c RGB) Hex() string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// mix blends two colors in sRGB space. This matches how Omarchy derives shades.
func mix(a, b RGB, t float64) RGB {
	t = clamp01(t)
	l := func(x, y uint8) uint8 { return uint8(math.Round(float64(x)*(1-t) + float64(y)*t)) }
	return RGB{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B)}
}

// blend interpolates in OKLab, which keeps gradients even in lightness.
func blend(a, b RGB, t float64) RGB {
	t = clamp01(t)
	la, lb := toOKLab(a), toOKLab(b)
	return fromOKLab([3]float64{
		la[0] + (lb[0]-la[0])*t,
		la[1] + (lb[1]-la[1])*t,
		la[2] + (lb[2]-la[2])*t,
	})
}

// gradient samples a multi-stop OKLab gradient at t in [0,1].
func gradient(stops []RGB, t float64) RGB {
	if len(stops) == 0 {
		return RGB{}
	}
	if len(stops) == 1 {
		return stops[0]
	}
	t = clamp01(t) * float64(len(stops)-1)
	i := int(t)
	if i >= len(stops)-1 {
		return stops[len(stops)-1]
	}
	return blend(stops[i], stops[i+1], t-float64(i))
}

func clamp01(t float64) float64 { return math.Max(0, math.Min(1, t)) }

func srgbToLinear(c uint8) float64 {
	v := float64(c) / 255
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func linearToSRGB(v float64) uint8 {
	v = clamp01(v)
	if v <= 0.0031308 {
		v *= 12.92
	} else {
		v = 1.055*math.Pow(v, 1/2.4) - 0.055
	}
	return uint8(math.Round(v * 255))
}

func toOKLab(c RGB) [3]float64 {
	r, g, b := srgbToLinear(c.R), srgbToLinear(c.G), srgbToLinear(c.B)
	l := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	return [3]float64{
		0.2104542553*l + 0.7936177850*m - 0.0040720468*s,
		1.9779984951*l - 2.4285922050*m + 0.4505937099*s,
		0.0259040371*l + 0.7827717662*m - 0.8086757660*s,
	}
}

func fromOKLab(lab [3]float64) RGB {
	l := lab[0] + 0.3963377774*lab[1] + 0.2158037573*lab[2]
	m := lab[0] - 0.1055613458*lab[1] - 0.0638541728*lab[2]
	s := lab[0] - 0.0894841775*lab[1] - 1.2914855480*lab[2]
	l, m, s = l*l*l, m*m*m, s*s*s
	return RGB{
		linearToSRGB(+4.0767416621*l - 3.3077115913*m + 0.2309699292*s),
		linearToSRGB(-1.2684380046*l + 2.6097574011*m - 0.3413193965*s),
		linearToSRGB(-0.0041960863*l - 0.7034186147*m + 1.7076147010*s),
	}
}

// luminance is the WCAG 2 relative luminance.
func luminance(c RGB) float64 {
	return 0.2126*srgbToLinear(c.R) + 0.7152*srgbToLinear(c.G) + 0.0722*srgbToLinear(c.B)
}

// contrast is the WCAG 2 contrast ratio, from 1 to 21.
func contrast(a, b RGB) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// contrastFrac maps a contrast ratio to a meter fill on a log scale, so that
// AA (4.5) fills half the meter and the maximum (21) fills all of it.
func contrastFrac(ratio float64) float64 {
	return clamp01(math.Log(ratio) / math.Log(21))
}

// wcagGrade names the WCAG level that a contrast ratio passes for body text.
func wcagGrade(ratio float64) string {
	switch {
	case ratio >= 7:
		return "AAA"
	case ratio >= 4.5:
		return "AA"
	case ratio >= 3:
		return "AA+"
	default:
		return "fail"
	}
}

// ── terminal escape output ──────────────────────────────────────────────────

type colorDepth int

const (
	depthTrue colorDepth = iota
	depth256
	depthNone
)

var depth = depthTrue

// canvas is painted behind every cell when a theme preview needs its own
// background. It is nil when the terminal background is already correct.
var canvas *RGB

const (
	escBold   = "\x1b[1m"
	escNoBold = "\x1b[22m"
)

func fg(c RGB) string {
	switch depth {
	case depthNone:
		return ""
	case depth256:
		return fmt.Sprintf("\x1b[38;5;%dm", to256(c))
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.R, c.G, c.B)
}

func bg(c RGB) string {
	switch depth {
	case depthNone:
		return ""
	case depth256:
		return fmt.Sprintf("\x1b[48;5;%dm", to256(c))
	}
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", c.R, c.G, c.B)
}

// rst clears all attributes and restores the canvas background.
func rst() string {
	if depth == depthNone {
		return ""
	}
	if canvas != nil {
		return "\x1b[0m" + bg(*canvas)
	}
	return "\x1b[0m"
}

func bold(s string) string {
	if depth == depthNone {
		return s
	}
	return escBold + s + escNoBold
}

// paint wraps s in a foreground color. The caller controls the reset.
func paint(c RGB, s string) string { return fg(c) + s }

// to256 maps a color to the closest entry of the xterm 256-color cube or ramp.
func to256(c RGB) int {
	steps := []int{0, 95, 135, 175, 215, 255}
	idx := func(v uint8) int {
		best, bi := 1<<30, 0
		for i, s := range steps {
			if d := abs(int(v) - s); d < best {
				best, bi = d, i
			}
		}
		return bi
	}
	ri, gi, bi := idx(c.R), idx(c.G), idx(c.B)
	cube := RGB{uint8(steps[ri]), uint8(steps[gi]), uint8(steps[bi])}
	gray := (int(c.R) + int(c.G) + int(c.B)) / 3
	gi2 := max(0, min(23, (gray-8+5)/10))
	grayC := uint8(8 + gi2*10)
	if dist(c, RGB{grayC, grayC, grayC}) < dist(c, cube) {
		return 232 + gi2
	}
	return 16 + 36*ri + 6*gi + bi
}

func dist(a, b RGB) int {
	dr, dg, db := int(a.R)-int(b.R), int(a.G)-int(b.G), int(a.B)-int(b.B)
	return dr*dr + dg*dg + db*db
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
