package ansi

import (
	"io"
	"math"
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

func TestConsoleSafe(t *testing.T) {
	if got := ConsoleSafe("€14.20 — loading…"); got != "EUR 14.20 - loading..." {
		t.Fatalf("ConsoleSafe = %q", got)
	}
	var b strings.Builder
	t.Setenv("TERM", "linux")
	if n, err := ConsoleWriter(&b).Write([]byte("€1")); n != len("€1") || err != nil || b.String() != "EUR 1" {
		t.Fatalf("console writer: %d %v %q", n, err, b.String())
	}
	t.Setenv("TERM", "xterm-256color")
	if w := ConsoleWriter(&b); w != io.Writer(&b) {
		t.Fatal("ConsoleWriter must pass through outside the Linux console")
	}
}
