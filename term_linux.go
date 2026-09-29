//go:build linux

package main

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

func ioctl(fd, req uintptr, arg unsafe.Pointer) bool {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	return e == 0
}

// termSize returns the terminal columns and rows, or zeros without a terminal.
func termSize() (int, int) {
	var ws struct{ Row, Col, X, Y uint16 }
	for _, f := range []*os.File{os.Stdout, os.Stderr, os.Stdin} {
		if ioctl(f.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&ws)) && ws.Col > 0 {
			return int(ws.Col), int(ws.Row)
		}
	}
	if tty, err := os.Open("/dev/tty"); err == nil {
		defer tty.Close()
		if ioctl(tty.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&ws)) && ws.Col > 0 {
			return int(ws.Col), int(ws.Row)
		}
	}
	return 0, 0
}

func isTerminal(f *os.File) bool {
	var t syscall.Termios
	return ioctl(f.Fd(), syscall.TCGETS, unsafe.Pointer(&t))
}

type termState struct {
	fd  uintptr
	old syscall.Termios
}

// rawMode turns off line buffering and echo. Signals stay on, so Ctrl+C
// still reaches the program.
func rawMode(f *os.File, vmin, vtime uint8) (*termState, bool) {
	var t syscall.Termios
	if !ioctl(f.Fd(), syscall.TCGETS, unsafe.Pointer(&t)) {
		return nil, false
	}
	st := &termState{fd: f.Fd(), old: t}
	t.Lflag &^= syscall.ICANON | syscall.ECHO
	t.Cc[syscall.VMIN] = vmin
	t.Cc[syscall.VTIME] = vtime
	if !ioctl(f.Fd(), syscall.TCSETS, unsafe.Pointer(&t)) {
		return nil, false
	}
	return st, true
}

func (s *termState) restore() {
	if s != nil {
		ioctl(s.fd, syscall.TCSETS, unsafe.Pointer(&s.old))
	}
}

// queryTerminalPalette asks the terminal for its 16 colors, foreground and
// background with OSC 4, 10 and 11. A device attributes request goes last:
// every terminal answers it, so its reply marks the end of the answers.
func queryTerminalPalette() map[string]string {
	if !isTerminal(os.Stdout) {
		return nil
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	defer tty.Close()
	st, ok := rawMode(tty, 0, 1)
	if !ok {
		return nil
	}
	defer st.restore()

	var q strings.Builder
	q.WriteString("\x1b]10;?\x1b\\\x1b]11;?\x1b\\")
	for i := range 16 {
		q.WriteString("\x1b]4;" + strconv.Itoa(i) + ";?\x1b\\")
	}
	q.WriteString("\x1b[c")
	if _, err := tty.WriteString(q.String()); err != nil {
		return nil
	}

	var buf []byte
	chunk := make([]byte, 1024)
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		n, _ := tty.Read(chunk)
		if n == 0 {
			break
		}
		buf = append(buf, chunk[:n]...)
		if endsWithDA(buf) {
			break
		}
	}
	return parseOSCReplies(string(buf))
}

func endsWithDA(b []byte) bool {
	s := string(b)
	i := strings.LastIndex(s, "\x1b[?")
	return i >= 0 && strings.Contains(s[i:], "c")
}

func parseOSCReplies(s string) map[string]string {
	out := map[string]string{}
	for part := range strings.SplitSeq(s, "\x1b]") {
		part = strings.TrimRight(strings.SplitN(part, "\x07", 2)[0], "\x1b\\")
		if i := strings.Index(part, "\x1b"); i >= 0 {
			part = part[:i]
		}
		fields := strings.Split(part, ";")
		var key, spec string
		switch {
		case len(fields) == 2 && fields[0] == "10":
			key, spec = "foreground", fields[1]
		case len(fields) == 2 && fields[0] == "11":
			key, spec = "background", fields[1]
		case len(fields) == 3 && fields[0] == "4":
			key, spec = "color"+fields[1], fields[2]
		default:
			continue
		}
		if c, ok := parseXColor(spec); ok {
			out[key] = c.Hex()
		}
	}
	if out["background"] == "" || out["color1"] == "" {
		return nil
	}
	if out["color8"] != "" {
		out["muted"] = out["color8"]
	}
	return out
}

// parseXColor reads the rgb:RRRR/GGGG/BBBB form that terminals reply with.
func parseXColor(s string) (RGB, bool) {
	s, ok := strings.CutPrefix(s, "rgb:")
	if !ok {
		return RGB{}, false
	}
	parts := strings.Split(s, "/")
	if len(parts) != 3 {
		return RGB{}, false
	}
	var v [3]uint8
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 16, 32)
		if err != nil || len(p) == 0 || len(p) > 4 {
			return RGB{}, false
		}
		maxv := uint64(1)<<(4*len(p)) - 1
		v[i] = uint8(n * 255 / maxv)
	}
	return RGB{v[0], v[1], v[2]}, true
}
