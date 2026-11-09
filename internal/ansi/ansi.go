// Package ansi holds the few SGR sequences Moneroid uses: Monero orange
// (#FF6600, exactly the fill of the official logo; 256-colour terminals show
// the nearest 202) and white. Colour never carries meaning
// alone (UI-06); every state is also written as text. Sequences are emitted
// as truecolor and downsampled by the writer (colorprofile) or by Bubble Tea,
// which also honour NO_COLOR and non-TTY output.
package ansi

import (
	"io"
	"os"
	"strings"
)

const (
	reset    = "\x1b[0m"
	orange   = "\x1b[38;2;255;102;0m"
	onOrange = "\x1b[1;97;48;2;255;102;0m"
	white    = "\x1b[1;97m"
	dim      = "\x1b[2m"
	red      = "\x1b[1;31m"
)

func wrap(seq, s string) string {
	if s == "" {
		return s
	}
	return seq + s + reset
}

func Orange(s string) string { return wrap(orange, s) }
func White(s string) string  { return wrap(white, s) }
func Dim(s string) string    { return wrap(dim, s) }
func Red(s string) string    { return wrap(red, s) }

// Banner renders s as white text on an orange bar (the htop-style header).
func Banner(s string) string { return wrap(onOrange, s) }

// Health colours a health level: ok is orange, starting/unknown plain, the rest red.
func Health(level string) string {
	switch level {
	case "ok":
		return Orange(level)
	case "degraded", "stopped":
		return Red(level)
	}
	return White(level)
}

// Unit colours a systemd state "active/running" style string.
func Unit(state string) string {
	switch {
	case strings.HasPrefix(state, "active/"):
		return Orange(state)
	case strings.HasPrefix(state, "failed"), strings.HasPrefix(state, "inactive"), strings.HasPrefix(state, "not-found"):
		return Red(state)
	}
	return White(state)
}

// Severity colours an issue severity; info stays dim.
func Severity(sev string) string {
	if sev == "info" {
		return Dim(sev)
	}
	return Red(sev)
}

// Bar draws a meter of width cells filled to frac (0..1); NaN or negative
// values give an empty bar. ascii selects a fallback without block glyphs.
func Bar(width int, frac float64, ascii bool) string {
	if width < 1 {
		return ""
	}
	if !(frac > 0) { // also catches NaN
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	n := int(frac*float64(width) + 0.5)
	full, empty := "█", "░"
	if ascii {
		full, empty = "#", "."
	}
	return Orange(strings.Repeat(full, n)) + Dim(strings.Repeat(empty, width-n))
}

// LinuxConsole reports the Linux virtual console (TERM=linux), e.g. the TUI on
// the server's own monitor. Its built-in kernel font holds only the 256 CP437
// glyphs, so €, —, – and … come out as a square there.
func LinuxConsole() bool { return os.Getenv("TERM") == "linux" }

var consoleGlyphs = strings.NewReplacer("€", "EUR ", "—", "-", "–", "-", "…", "...")

// ConsoleSafe spells out the glyphs Moneroid prints that the kernel font lacks
// (the rest it uses, · ≈ ← ↑ → ↓ § █ ▒ ░, are in CP437).
func ConsoleSafe(s string) string { return consoleGlyphs.Replace(s) }

// ConsoleWriter is w, passed through ConsoleSafe on the Linux console.
func ConsoleWriter(w io.Writer) io.Writer {
	if !LinuxConsole() {
		return w
	}
	return consoleWriter{w}
}

type consoleWriter struct{ w io.Writer }

func (c consoleWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(c.w, ConsoleSafe(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}
