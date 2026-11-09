package ansi

import (
	"io"
	"math"
	"os"
	"strings"
	"testing"
)

func TestBar(t *testing.T) {
	if got := Bar(10, 0.5, true); !strings.Contains(got, "#####") || !strings.Contains(got, ".....") || strings.Contains(got, "######") {
		t.Fatalf("half bar: %q", got)
	}
	for _, f := range []float64{math.NaN(), -1, 0} {
		if got := Bar(4, f, true); strings.Contains(got, "#") || !strings.Contains(got, "....") {
			t.Fatalf("empty bar for %v: %q", f, got)
		}
	}
	if got := Bar(4, 7, true); !strings.Contains(got, "####") {
		t.Fatalf("overflow must clamp: %q", got)
	}
	if Bar(0, 1, true) != "" || Orange("") != "" {
		t.Fatal("empty inputs must stay empty")
	}
}

func TestFont(t *testing.T) {
	if got := KernelFont.Safe("€14.20 — loading… ≈ $1 · ░"); got != "EUR 14.20 - loading... ≈ $1 · ░" {
		t.Fatalf("KernelFont.Safe = %q", got)
	}
	lat15 := NewFont("€—–…·≈←↑→↓§░▒█") // Debian console-setup Lat15-Fixed16
	if got := lat15.Safe("€1 ▓ ✓"); got != "€1 # ?" {
		t.Fatalf("lat15.Safe = %q", got)
	}
	for f, want := range map[*Font]string{{}: "▁▂▃▄▅▆▇█", &KernelFont: "░░▒▒▓▓██", &lat15: "░░░▒▒▒██", {set: map[rune]bool{}}: ".:-=+*#@"} {
		if got := string(f.Ramp()); got != want {
			t.Fatalf("ramp %q, want %q", got, want)
		}
	}
	var b strings.Builder
	if n, err := KernelFont.Writer(&b).Write([]byte("€1")); n != len("€1") || err != nil || b.String() != "EUR 1" {
		t.Fatalf("font writer: %d %v %q", n, err, b.String())
	}
	if w := (Font{}).Writer(&b); w != io.Writer(&b) || (Font{}).Safe("€") != "€" {
		t.Fatal("the zero Font must pass everything through")
	}
	r, w, _ := os.Pipe()
	defer r.Close()
	defer w.Close()
	t.Setenv("TERM", "xterm-256color")
	if ConsoleFont(w.Fd()).set != nil {
		t.Fatal("outside the Linux console the font is unrestricted")
	}
	t.Setenv("TERM", "linux")
	if f := ConsoleFont(w.Fd()); f.Has('€') || !f.Has('▓') {
		t.Fatal("a non-console fd on TERM=linux must fall back to the kernel font")
	}
}
