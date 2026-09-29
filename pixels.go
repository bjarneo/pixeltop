package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/webp"
)

// pixmap is a small RGB image. Two pixel rows fill one terminal row.
type pixmap struct {
	w, h int
	px   []RGB
}

func newPixmap(w, h int) *pixmap { return &pixmap{w: w, h: h, px: make([]RGB, w*h)} }

func (p *pixmap) at(x, y int) RGB     { return p.px[y*p.w+x] }
func (p *pixmap) set(x, y int, c RGB) { p.px[y*p.w+x] = c }

// lines renders the pixmap with upper half blocks: the foreground paints the
// top pixel and the background paints the bottom pixel.
func (p *pixmap) lines() []string {
	out := make([]string, 0, (p.h+1)/2)
	for y := 0; y < p.h; y += 2 {
		var b strings.Builder
		var lastF, lastB RGB
		first := true
		for x := range p.w {
			top := p.at(x, y)
			bot := top
			if y+1 < p.h {
				bot = p.at(x, y+1)
			}
			if first || top != lastF {
				b.WriteString(fg(top))
			}
			if first || bot != lastB {
				b.WriteString(bg(bot))
			}
			first, lastF, lastB = false, top, bot
			if depth == depthNone {
				b.WriteString(" ")
			} else {
				b.WriteString("▀")
			}
		}
		b.WriteString(rst())
		out = append(out, b.String())
	}
	return out
}

// ── wallpaper thumbnails ────────────────────────────────────────────────────

func cacheDir() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		base = filepath.Join(home(), ".cache")
	}
	return filepath.Join(base, "pixeltop")
}

// thumbnail returns the image at path scaled and cropped to w by h pixels.
// Results are cached, because a full size wallpaper takes long to decode.
func thumbnail(path string, w, h int) (*pixmap, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum(fmt.Appendf(nil, "%s|%d|%d|%dx%d|v1", path, st.Size(), st.ModTime().UnixNano(), w, h))
	cached := filepath.Join(cacheDir(), "thumbs", hex.EncodeToString(sum[:10])+".png")

	if f, err := os.Open(cached); err == nil {
		img, err := png.Decode(f)
		f.Close()
		if err == nil && img.Bounds().Dx() == w && img.Bounds().Dy() == h {
			return fromImage(img), nil
		}
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	pm := downsample(img, w, h)

	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err == nil {
		if out, err := os.Create(cached); err == nil {
			_ = png.Encode(out, pm.image())
			out.Close()
		}
	}
	return pm, nil
}

// imageSize reads only the header of an image file.
func imageSize(path string) (int, int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func fromImage(img image.Image) *pixmap {
	b := img.Bounds()
	p := newPixmap(b.Dx(), b.Dy())
	for y := range p.h {
		for x := range p.w {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			p.set(x, y, RGB{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8)})
		}
	}
	return p
}

func (p *pixmap) image() image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, p.w, p.h))
	for y := range p.h {
		for x := range p.w {
			c := p.at(x, y)
			img.SetNRGBA(x, y, color.NRGBA{c.R, c.G, c.B, 255})
		}
	}
	return img
}

// downsample crops img to the w:h aspect ratio around its center, then
// averages every source pixel into its target cell.
func downsample(img image.Image, w, h int) *pixmap {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	cw, ch := sw, sh
	if float64(sw)/float64(sh) > float64(w)/float64(h) {
		cw = sh * w / h
	} else {
		ch = sw * h / w
	}
	x0, y0 := b.Min.X+(sw-cw)/2, b.Min.Y+(sh-ch)/2

	// Sample about eight source pixels per target pixel on each axis.
	step := max(1, min(cw/(w*8), ch/(h*8)))
	sums := make([][4]uint64, w*h)

	read := pixelReader(img)
	for y := 0; y < ch; y += step {
		ty := y * h / ch
		for x := 0; x < cw; x += step {
			tx := x * w / cw
			r, g, bl := read(x0+x, y0+y)
			s := &sums[ty*w+tx]
			s[0] += uint64(r)
			s[1] += uint64(g)
			s[2] += uint64(bl)
			s[3]++
		}
	}
	p := newPixmap(w, h)
	for i, s := range sums {
		if s[3] > 0 {
			p.px[i] = RGB{uint8(s[0] / s[3]), uint8(s[1] / s[3]), uint8(s[2] / s[3])}
		}
	}
	return p
}

// pixelReader returns a fast accessor for the common decoded image types.
func pixelReader(img image.Image) func(x, y int) (uint8, uint8, uint8) {
	switch m := img.(type) {
	case *image.YCbCr:
		return func(x, y int) (uint8, uint8, uint8) {
			yi, ci := m.YOffset(x, y), m.COffset(x, y)
			return color.YCbCrToRGB(m.Y[yi], m.Cb[ci], m.Cr[ci])
		}
	case *image.NRGBA:
		return func(x, y int) (uint8, uint8, uint8) {
			i := m.PixOffset(x, y)
			return m.Pix[i], m.Pix[i+1], m.Pix[i+2]
		}
	case *image.RGBA:
		return func(x, y int) (uint8, uint8, uint8) {
			i := m.PixOffset(x, y)
			return m.Pix[i], m.Pix[i+1], m.Pix[i+2]
		}
	}
	return func(x, y int) (uint8, uint8, uint8) {
		r, g, b, _ := img.At(x, y).RGBA()
		return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)
	}
}

// ── pixel font ──────────────────────────────────────────────────────────────

var glyphs = map[rune][5]string{
	'p': {"##.", "#.#", "##.", "#..", "#.."},
	'i': {"###", ".#.", ".#.", ".#.", "###"},
	'x': {"#.#", "#.#", ".#.", "#.#", "#.#"},
	'e': {"###", "#..", "##.", "#..", "###"},
	'l': {"#..", "#..", "#..", "#..", "###"},
	't': {"###", ".#.", ".#.", ".#.", ".#."},
	'o': {".#.", "#.#", "#.#", "#.#", ".#."},
}

// wordmark draws "pixeltop" in the pixel font on top of p, centered, with a
// left to right gradient.
func wordmark(p *pixmap, stops []RGB) {
	word := []rune("pixeltop")
	ww := len(word)*4 - 1
	ox, oy := (p.w-ww)/2, (p.h-5)/2
	if oy%2 == 1 {
		oy-- // keep the letters aligned to terminal rows
	}
	for i, r := range word {
		g := glyphs[r]
		for gy := range 5 {
			for gx := range 3 {
				x, y := ox+i*4+gx, oy+gy
				if g[gy][gx] != '#' || x < 0 || y < 0 || x >= p.w || y >= p.h {
					continue
				}
				p.set(x, y, gradient(stops, float64(x-ox)/float64(max(1, ww-1))))
			}
		}
	}
}

// mosaic is the artwork for a theme without a wallpaper: a soft vertical
// gradient in the theme background with the wordmark on top.
func mosaic(t *Theme, w, h int) *pixmap {
	p := newPixmap(w, h)
	top, bottom := t.LighterBackground, t.DarkerBackground
	if contrast(top, bottom) < 1.08 {
		top = blend(t.Background, t.Accent, 0.18)
	}
	for y := range h {
		for x := range w {
			v := float64(y) / float64(max(1, h-1))
			// A 2x2 ordered dither keeps the gradient from banding.
			if (x+y)%2 == 0 {
				v += 0.04
			}
			p.set(x, y, blend(top, bottom, v))
		}
	}
	wordmark(p, []RGB{t.Magenta, t.Accent, t.Cyan})
	return p
}

// ── Omarchy logo ────────────────────────────────────────────────────────────

// omarchyLogo is the Omarchy mark on its 13 by 13 grid. It is sampled from
// icon.txt in the Omarchy repository.
var omarchyLogo = [13]string{
	"#############",
	"#.....#.....#",
	"#.#####..##.#",
	"#.#.......#.#",
	"#.#.......#.#",
	"#.#.......#.#",
	"###.......#.#",
	"#.#.......#.#",
	"#.#.......#.#",
	"#.#.......#.#",
	"#.#########.#",
	"#.....#.....#",
	"#######.#####",
}

// logoScale returns the largest scale at which the logo fits w cells and
// maxRows rows. Scale 2 draws each logo pixel as a full block. Scale 1 uses
// half blocks. A maxRows of 0 sets no row limit.
func logoScale(w, maxRows int) int {
	if w >= 26 && (maxRows <= 0 || maxRows >= 13) {
		return 2
	}
	return 1
}

// logoLines draws the Omarchy mark at scale u, centered in w cells. The
// color steps along a diagonal gradient, one color for each logo pixel.
func logoLines(stops []RGB, w, u int) []string {
	size := 13 * u
	on := func(px, py int) (bool, RGB) {
		if py >= size {
			return false, RGB{}
		}
		ux, uy := px/u, py/u
		if omarchyLogo[uy][ux] != '#' {
			return false, RGB{}
		}
		return true, gradient(stops, float64(ux+uy)/24)
	}
	indent := strings.Repeat(" ", max(0, (w-size)/2))
	out := make([]string, 0, (size+1)/2)
	for row := 0; row < size; row += 2 {
		var b strings.Builder
		b.WriteString(indent)
		for x := range size {
			top, tc := on(x, row)
			bot, bc := on(x, row+1)
			switch {
			case top && bot && (tc == bc || depth == depthNone):
				b.WriteString(fg(tc) + "█")
			case top && bot:
				b.WriteString(fg(tc) + bg(bc) + "▀" + rst())
			case top:
				b.WriteString(fg(tc) + "▀")
			case bot:
				b.WriteString(fg(bc) + "▄")
			default:
				b.WriteByte(' ')
			}
		}
		b.WriteString(rst())
		out = append(out, b.String())
	}
	return out
}
