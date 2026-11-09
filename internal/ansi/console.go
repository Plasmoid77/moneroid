package ansi

import (
	"io"
	"os"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

// Font is the set of characters the terminal can draw. The zero Font draws
// everything (a normal terminal emulator); on the Linux console (TERM=linux,
// e.g. the server's own monitor) it is the loaded console font, which holds
// only 256 or 512 glyphs, and anything outside it shows as a square (UI-06).
type Font struct{ set map[rune]bool }

// NewFont is a font with exactly the characters of chars plus printable ASCII.
func NewFont(chars string) Font {
	f := Font{set: map[rune]bool{}}
	for _, r := range chars {
		f.set[r] = true
	}
	return f
}

// KernelFont is what Moneroid needs from the kernel's built-in CP437 font, the
// console's font when console-setup/setfont never loaded another one.
var KernelFont = NewFont("·≈←↑→↓§░▒▓█▄─▀│")

// Has reports whether f draws r.
func (f Font) Has(r rune) bool { return f.set == nil || r < 0x7f || f.set[r] }

// spelled stands in for a character the font lacks; anything else becomes "?".
var spelled = map[rune]string{
	'€': "EUR ", '—': "-", '–': "-", '…': "...", '≈': "~", '·': "|", '│': "|", '§': "sec. ",
	'←': "<", '→': ">", '↑': "^", '↓': "v", '█': "#", '▓': "#", '▒': "+", '░': ".",
}

// Safe spells out the characters of s that f cannot draw.
func (f Font) Safe(s string) string {
	if f.set == nil {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case f.Has(r):
			b.WriteRune(r)
		case spelled[r] != "":
			b.WriteString(spelled[r])
		default:
			b.WriteByte('?')
		}
	}
	return b.String()
}

// Ramp is the 8-step sparkline ramp f draws best: eighth blocks, else a thin
// trace of glyphs at three heights (_ ─ ¯). Solid shades are avoided: console
// fonts have no gap between rows, so they merged with a full meter above.
func (f Font) Ramp() []rune {
	if eighths := []rune("▁▂▃▄▅▆▇█"); f.all(eighths) {
		return eighths
	}
	mid, top := '-', '^'
	if f.Has('─') {
		mid = '─'
	}
	if f.Has('¯') {
		top = '¯'
	} else if f.Has('▀') {
		top = '▀'
	}
	return []rune{'_', '_', '_', mid, mid, mid, top, top}
}

func (f Font) all(rs []rune) bool {
	for _, r := range rs {
		if !f.Has(r) {
			return false
		}
	}
	return true
}

// Writer is w passed through Safe.
func (f Font) Writer(w io.Writer) io.Writer {
	if f.set == nil {
		return w
	}
	return fontWriter{w, f}
}

type fontWriter struct {
	w io.Writer
	f Font
}

func (c fontWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(c.w, c.f.Safe(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// ConsoleFont is the font of the terminal on fd: the zero Font unless
// TERM=linux; there, the console font's Unicode map asked from the kernel
// (GIO_UNIMAP), or KernelFont when the kernel will not tell (fd not a console).
func ConsoleFont(fd uintptr) Font {
	if os.Getenv("TERM") != "linux" {
		return Font{}
	}
	if set, ok := unimap(fd); ok {
		return Font{set: set}
	}
	return KernelFont
}

const gioUnimap = 0x4B66 // linux/kd.h

type unipair struct{ unicode, fontpos uint16 }

type unimapdesc struct { // struct unimapdesc: the Go layout matches the C one
	count   uint16
	entries *unipair
}

func unimap(fd uintptr) (map[rune]bool, bool) {
	var d unimapdesc // count 0 first: the kernel answers ENOMEM with the size
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, gioUnimap, uintptr(unsafe.Pointer(&d))); (e != 0 && e != syscall.ENOMEM) || d.count == 0 {
		return nil, false
	}
	buf := make([]unipair, d.count)
	d.entries = &buf[0]
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, gioUnimap, uintptr(unsafe.Pointer(&d)))
	runtime.KeepAlive(buf)
	if e != 0 {
		return nil, false
	}
	set := make(map[rune]bool, len(buf))
	for _, p := range buf[:min(int(d.count), len(buf))] {
		set[rune(p.unicode)] = true
	}
	return set, true
}
