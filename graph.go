package main

import (
	"math"
	"strings"
)

// Braille dot bits for the left and right column, from the top row down.
var (
	dotLeft  = [4]rune{0x01, 0x02, 0x04, 0x40}
	dotRight = [4]rune{0x08, 0x10, 0x20, 0x80}
)

// graph draws values from 0 to 1 as a filled braille area graph, as btop
// does. Each cell holds two samples and four levels, so a graph that is w
// cells wide shows the last 2*w samples. The color moves along stops from
// the bottom row to the top row.
func graph(values []float64, w, h int, stops []RGB, t *Theme) []string {
	n := 2 * w
	v := make([]float64, n)
	if len(values) >= n {
		copy(v, values[len(values)-n:])
	} else {
		copy(v[n-len(values):], values)
	}
	levels := 4 * h
	lvl := make([]int, n)
	for i, x := range v {
		lvl[i] = int(math.Round(clamp01(x) * float64(levels)))
		if x > 0.001 && lvl[i] == 0 {
			lvl[i] = 1
		}
	}

	out := make([]string, h)
	for row := range h {
		var b strings.Builder
		b.WriteString(fg(gradient(stops, 1-(float64(row)+0.5)/float64(h))))
		for cx := range w {
			var r rune
			for dy := range 4 {
				fromBottom := levels - 1 - (row*4 + dy)
				if fromBottom < lvl[2*cx] {
					r |= dotLeft[dy]
				}
				if fromBottom < lvl[2*cx+1] {
					r |= dotRight[dy]
				}
			}
			if r == 0 {
				b.WriteByte(' ')
			} else {
				b.WriteRune(0x2800 + r)
			}
		}
		out[row] = b.String()
	}
	return out
}

// normalize scales values so that the largest one is 1. The floor keeps an
// idle link from filling the whole graph with noise.
func normalize(values []float64, floor float64) []float64 {
	peak := floor
	for _, v := range values {
		peak = math.Max(peak, v)
	}
	out := make([]float64, len(values))
	for i, v := range values {
		out[i] = v / peak
	}
	return out
}
