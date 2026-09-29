package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

var version = "0.1.0"

const usageText = `pixeltop: a live system monitor that shows your theme

Usage:
  pixeltop [command] [flags]

Commands:
  (none)          Show the dashboard and refresh it on every interval, as btop
                  does. Press + or - to change the interval. Press q to quit
  palette         Print every theme color with hex values, contrast and a code preview
  themes [text]   Print a gallery of the installed Omarchy themes. Text filters by name

Flags:
  -t, --theme NAME     Use an installed theme, a theme folder or a colors.toml file
  -i, --interval MS    Set the refresh interval in milliseconds (default 1000)
  -1, --once           Print one snapshot of the dashboard and exit
  -w, --width N        Set the output width. The default is the terminal width
      --color MODE     Set the color output: auto, truecolor, 256 or none (default auto)
      --bg             Paint the theme background behind the output
      --no-bg          Never paint the theme background
      --no-logo        Hide the Omarchy logo
  -v, --version        Print the version

The dashboard adapts to the terminal size, down to 24 columns and 6 rows.

Theme sources, in order: the active Omarchy theme, the colors that the
terminal reports, then the built-in pixeltop palette. The dashboard reloads
the theme when you switch themes or wallpapers, or when you edit the colors.

Examples:
  pixeltop
  pixeltop -i 500
  pixeltop --once
  pixeltop --theme tokyo-night
  pixeltop --theme ~/themes/mine/colors.toml
  pixeltop palette --theme kanagawa
  pixeltop themes kanagawa
`

type options struct {
	theme    string
	width    int
	interval int
	color    string
	paint    bool
	noPaint  bool
	noLogo   bool
	once     bool
	version  bool
}

func parseArgs(args []string) (options, []string, error) {
	var o options
	fs := flag.NewFlagSet("pixeltop", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usageText) }
	for _, n := range []string{"t", "theme"} {
		fs.StringVar(&o.theme, n, "", "")
	}
	for _, n := range []string{"w", "width"} {
		fs.IntVar(&o.width, n, 0, "")
	}
	for _, n := range []string{"i", "interval"} {
		fs.IntVar(&o.interval, n, 1000, "")
	}
	for _, n := range []string{"v", "version"} {
		fs.BoolVar(&o.version, n, false, "")
	}
	for _, n := range []string{"1", "once"} {
		fs.BoolVar(&o.once, n, false, "")
	}
	fs.StringVar(&o.color, "color", "auto", "")
	fs.BoolVar(&o.paint, "bg", false, "")
	fs.BoolVar(&o.noPaint, "no-bg", false, "")
	fs.BoolVar(&o.noLogo, "no-logo", false, "")

	// Accept flags before and after positional arguments.
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return o, nil, err
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	return o, pos, nil
}

func main() {
	o, pos, err := parseArgs(os.Args[1:])
	if err == flag.ErrHelp {
		return
	}
	if err != nil {
		os.Exit(2)
	}
	if o.version {
		fmt.Println("pixeltop", version)
		return
	}
	if err := execute(o, pos); err != nil {
		fmt.Fprintln(os.Stderr, "pixeltop:", err)
		os.Exit(1)
	}
}

func execute(o options, pos []string) error {
	cmd := ""
	if len(pos) > 0 {
		cmd = pos[0]
	}
	switch cmd {
	case "", "palette", "colors", "themes", "gallery", "live", "top":
	case "help":
		fmt.Print(usageText)
		return nil
	default:
		return fmt.Errorf("unknown command %q. Run `pixeltop --help` for the list of commands", cmd)
	}

	switch o.color {
	case "auto":
		depth = detectDepth()
	case "truecolor", "24bit":
		depth = depthTrue
	case "256":
		depth = depth256
	case "none", "off":
		depth = depthNone
	default:
		return fmt.Errorf("unknown color mode %q. Use auto, truecolor, 256 or none", o.color)
	}

	cols, _ := termSize()
	width := o.width
	if width <= 0 {
		width = cols
		if width <= 0 {
			width = 100
		}
	}

	if cmd == "themes" || cmd == "gallery" {
		filter := ""
		if len(pos) > 1 {
			filter = strings.Join(pos[1:], " ")
		}
		return emit(gallery(filter, width), 0)
	}

	t, err := loadTheme(o.theme, depth != depthNone)
	if err != nil {
		return err
	}
	setCanvas(o, t)

	// The painted margin takes four columns.
	if canvas != nil && o.width <= 0 {
		width -= 4
	}

	if cmd == "palette" || cmd == "colors" {
		return emit(paletteView(t, min(width, 96)), min(width, 96))
	}

	// The dashboard is live in a terminal. Piped output gets one snapshot.
	if !o.once && isTerminal(os.Stdout) {
		return runLive(o, t)
	}
	if (cmd == "live" || cmd == "top") && !o.once {
		return fmt.Errorf("live mode needs a terminal. Use --once to print one snapshot")
	}
	in := collect(120 * time.Millisecond)
	lay := layout{}
	if o.noLogo {
		lay.d.logo = 2
	}
	lines := dashboard(t, in, width, 0, lay)
	return emit(lines, maxWidth(lines))
}

func maxWidth(lines []string) int {
	w := 0
	for _, l := range lines {
		w = max(w, vlen(l))
	}
	return w
}

// setCanvas decides if the output paints the theme background. A preview of
// a theme that is not active paints it, so the colors show as they do on a
// desktop that uses the theme.
func setCanvas(o options, t *Theme) {
	canvas = nil
	if o.noPaint || depth == depthNone {
		return
	}
	if o.paint || (t.Preview && t.Source != "terminal") {
		c := t.Background
		canvas = &c
	}
}

// emit prints lines. With a canvas it adds a painted margin around them.
func emit(lines []string, w int) error {
	out := bufio.NewWriterSize(os.Stdout, 1<<16)
	defer out.Flush()
	if canvas == nil {
		for _, l := range lines {
			out.WriteString(l + resetAll() + "\n")
		}
		return nil
	}
	margin := rst() + strings.Repeat(" ", 2)
	blank := rst() + strings.Repeat(" ", w+4) + resetAll() + "\n"
	out.WriteString(blank)
	for _, l := range lines {
		out.WriteString(margin + pad(l, w) + margin + resetAll() + "\n")
	}
	out.WriteString(blank)
	return nil
}

func resetAll() string {
	if depth == depthNone {
		return ""
	}
	return "\x1b[0m"
}

func detectDepth() colorDepth {
	if os.Getenv("NO_COLOR") != "" {
		return depthNone
	}
	switch strings.ToLower(os.Getenv("COLORTERM")) {
	case "truecolor", "24bit":
		return depthTrue
	}
	term := os.Getenv("TERM")
	if term == "dumb" {
		return depthNone
	}
	if term == "linux" || os.Getenv("TERM_PROGRAM") == "Apple_Terminal" {
		return depth256
	}
	return depthTrue
}
