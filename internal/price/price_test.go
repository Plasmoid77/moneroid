package price

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetch(t *testing.T) {
	cases := map[string]struct {
		code     int
		body     string
		usd, eur float64 // 0 = nil
		fail     bool
	}{
		"both":       {200, `{"monero":{"usd":599.02,"eur":517.4}}`, 599.02, 517.4, false},
		"usd only":   {200, `{"monero":{"usd":599.02}}`, 599.02, 0, false},
		"negative":   {200, `{"monero":{"usd":-1,"eur":0}}`, 0, 0, true},
		"empty":      {200, `{}`, 0, 0, true},
		"garbage":    {200, `<html>`, 0, 0, true},
		"rate-limit": {429, `{}`, 0, 0, true},
	}
	for name, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.code)
			w.Write([]byte(c.body))
		}))
		now := time.Unix(1794188828, 0)
		q, err := Fetch(context.Background(), srv.Client(), srv.URL, now)
		srv.Close()
		if c.fail {
			if err == nil {
				t.Fatalf("%s: want an error, got %+v", name, q)
			}
			continue
		}
		if err != nil || !eq(q.USD, c.usd) || !eq(q.EUR, c.eur) || !q.At.Equal(now) {
			t.Fatalf("%s: %+v %v", name, q, err)
		}
	}
}

// A slow or refused answer reads as one short line, without Go's URL dump.
func TestFetchErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	c := srv.Client()
	c.Timeout = 50 * time.Millisecond
	_, err := Fetch(context.Background(), c, srv.URL, time.Now())
	srv.Close()
	if err == nil || err.Error() != "CoinGecko: no answer in time" {
		t.Fatalf("slow: %v", err)
	}
	_, err = Fetch(context.Background(), &http.Client{}, srv.URL, time.Now()) // closed: refused
	if err == nil || strings.Contains(err.Error(), srv.URL) || !strings.HasPrefix(err.Error(), "CoinGecko: ") {
		t.Fatalf("refused: %v", err)
	}
}

func eq(p *float64, v float64) bool {
	if v == 0 {
		return p == nil
	}
	return p != nil && *p == v
}

func TestPerDay(t *testing.T) {
	// 14 360 H/s, difficulty 821.57 G, reward 0.6058 XMR: about 0.000915 XMR a day.
	got := PerDay(14360, 821571146720, 605805580000)
	if math.Abs(got-0.000915) > 0.000002 {
		t.Fatalf("PerDay = %.7f", got)
	}
	if PerDay(0, 1, 1) != 0 || PerDay(1, 0, 1) != 0 || PerDay(1, 1, 0) != 0 || PerDay(math.NaN(), 1, 1) != 0 {
		t.Fatal("missing inputs must give 0")
	}
}
