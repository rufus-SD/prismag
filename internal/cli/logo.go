package cli

import (
	"fmt"
	"os"
	"strings"
)

// logoLines is the PRISMAG wordmark with a small prism accent on the left.
// Rendered with a left-to-right rainbow so it reads like white light split
// through a prism into a spectrum — the whole idea of the tool.
var logoLines = []string{
	`  ╱╲     ___  ___ ___ ___ __  __   _   ___ `,
	` ╱░░╲   | _ \| _ \_ _/ __|  \/  | /_\ / __|`,
	`╱░░░░╲  |  _/|   /| |\__ \ |\/| |/ _ \ (_ |`,
	`╲░░░░╱  |_|  |_|_\___|___/_|  |_/_/ \_\___|`,
	` ╲░░╱   per-block model routing`,
	`  ╲╱    one prompt · the right model per block`,
}

// Banner returns the colorized logo (or plain text when color is disabled).
func Banner() string {
	maxw := 0
	for _, l := range logoLines {
		if w := len([]rune(l)); w > maxw {
			maxw = w
		}
	}
	color := colorEnabled()
	var b strings.Builder
	b.WriteByte('\n')
	for _, line := range logoLines {
		if !color {
			b.WriteString(line)
			b.WriteByte('\n')
			continue
		}
		for i, r := range []rune(line) {
			if r == ' ' {
				b.WriteRune(r)
				continue
			}
			rr, gg, bb := spectrum(float64(i) / float64(maxw))
			fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm%c", rr, gg, bb, r)
		}
		b.WriteString("\x1b[0m\n")
	}
	return b.String()
}

// colorEnabled reports whether to emit ANSI color: respects NO_COLOR and only
// colors when stdout is a terminal.
func colorEnabled() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// spectrum maps t in [0,1] to an RGB color sweeping red→violet (a rainbow).
func spectrum(t float64) (int, int, int) {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	// Hue from 0° (red) to 280° (violet).
	return hsvToRGB(t*280.0, 0.85, 1.0)
}

func hsvToRGB(h, s, v float64) (int, int, int) {
	c := v * s
	x := c * (1 - abs(mod(h/60.0, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return int((r + m) * 255), int((g + m) * 255), int((b + m) * 255)
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func mod(a, b float64) float64 {
	return a - b*float64(int(a/b))
}
