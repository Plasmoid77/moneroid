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
		p        Provider
	}{
		"both":             {200, `{"monero":{"usd":599.02,"eur":517.4}}`, 599.02, 517.4, false, CoinGecko},
		"paprika":          {200, `{"id":"xmr-monero","quotes":{"EUR":{"price":528.47,"volume_24h":1.3e8},"USD":{"price":594.3}}}`, 594.3, 528.47, false, CoinPaprika},
		"paprika eur only": {200, `{"quotes":{"EUR":{"price":528.47}}}`, 0, 528.47, false, CoinPaprika},
		"gecko fresh":      {200, `{"monero":{"usd":599.02,"eur":517.4,"last_updated_at":1794188800}}`, 599.02, 517.4, false, CoinGecko},
		"gecko stale":      {200, `{"monero":{"usd":599.02,"eur":517.4,"last_updated_at":1794180000}}`, 0, 0, true, CoinGecko},
		"paprika fresh":    {200, `{"last_updated":"2026-11-09T01:30:00Z","quotes":{"USD":{"price":594.3}}}`, 594.3, 0, false, CoinPaprika},
		"paprika stale":    {200, `{"last_updated":"2026-11-08T22:00:00Z","quotes":{"USD":{"price":594.3}}}`, 0, 0, true, CoinPaprika},
		"paprika bad time": {200, `{"last_updated":"yesterday","quotes":{"USD":{"price":594.3}}}`, 0, 0, true, CoinPaprika},
		"kraken":           {200, `{"error":[],"result":{"XXMRZEUR":{"a":["530.09","2","2.000"],"c":["528.58","0.45"]},"XXMRZUSD":{"c":["594.30","0.1"]}}}`, 594.3, 528.58, false, Kraken},
		"kraken error":     {200, `{"error":["EQuery:Unknown asset pair"]}`, 0, 0, true, Kraken},
		"kraken garbage":   {200, `{"error":[],"result":{"XXMRZUSD":{"c":["n/a"]}}}`, 0, 0, true, Kraken},
		"paprika empty":    {200, `{"error":"id not found"}`, 0, 0, true, CoinPaprika},
		"usd only":         {200, `{"monero":{"usd":599.02}}`, 599.02, 0, false, CoinGecko},
		"negative":         {200, `{"monero":{"usd":-1,"eur":0}}`, 0, 0, true, CoinGecko},
		"empty":            {200, `{}`, 0, 0, true, CoinGecko},
		"garbage":          {200, `<html>`, 0, 0, true, CoinGecko},
		"rate-limit":       {429, `{}`, 0, 0, true, CoinGecko},
	}
	for name, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.code)
			w.Write([]byte(c.body))
		}))
		now := time.Unix(1794188828, 0)
		p := c.p
		p.URL = srv.URL
		q, err := Fetch(context.Background(), srv.Client(), p, now)
		srv.Close()
		if c.fail {
			if err == nil {
				t.Fatalf("%s: want an error, got %+v", name, q)
			}
			continue
		}
		if err != nil || !eq(q.USD, c.usd) || !eq(q.EUR, c.eur) || !q.At.Equal(now) || q.Source != p.Name {
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
	slow := CoinGecko
	slow.URL = srv.URL
	_, err := Fetch(context.Background(), c, slow, time.Now())
	srv.Close()
	if err == nil || err.Error() != "CoinGecko: no answer in time" {
		t.Fatalf("slow: %v", err)
	}
	_, err = Fetch(context.Background(), &http.Client{}, slow, time.Now()) // closed: refused
	if err == nil || strings.Contains(err.Error(), srv.URL) || !strings.HasPrefix(err.Error(), "CoinGecko: ") {
		t.Fatalf("refused: %v", err)
	}
}

// All providers are asked; each answer stays in its place, a failure does not hide the others.
func TestFetchAll(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(429) }))
	defer down.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"quotes":{"USD":{"price":594.3},"EUR":{"price":528.47}}}`))
	}))
	defer up.Close()
	g, p := CoinGecko, CoinPaprika
	g.URL, p.URL = down.URL, up.URL
	r := FetchAll(context.Background(), &http.Client{}, []Provider{g, p}, time.Now)
	if len(r) != 2 || r[0].Err == nil || r[0].Err.Error() != "CoinGecko: HTTP 429" || r[1].Err != nil || r[1].Quote.Source != "CoinPaprika" || *r[1].Quote.USD != 594.3 {
		t.Fatalf("results: %+v", r)
	}
	if len(Providers) != 3 || Providers[0].Name != "CoinGecko" || Providers[1].Name != "Kraken" || Providers[2].Name != "CoinPaprika" {
		t.Fatal("CoinGecko, Kraken, CoinPaprika")
	}
}

func TestMedian(t *testing.T) {
	t0 := time.Unix(1794188828, 0)
	q := func(usd, eur float64, at time.Time) *Quote {
		return &Quote{USD: positive(usd), EUR: positive(eur), At: at}
	}
	m := Median([]*Quote{q(592.95, 528.29, t0), q(594.3, 0, t0.Add(time.Second)), nil, q(592.71, 528.17, t0)})
	if !eq(m.USD, 592.95) || !eq(m.EUR, 528.23) || !m.At.Equal(t0.Add(time.Second)) {
		t.Fatalf("median: %v %v %v", *m.USD, *m.EUR, m.At)
	}
	if Median([]*Quote{nil}) != nil || Median(nil) != nil {
		t.Fatal("no quotes, no median")
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
