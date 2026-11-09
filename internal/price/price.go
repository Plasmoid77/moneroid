// Package price reads the XMR exchange rate in USD and EUR for the TUI
// (spec PRICE-01..03) from three providers. Public GETs, no key, no address or other identifying
// data in the request; the rate is display-only.
package price

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Plasmoid77/moneroid/internal/ansi"
)

// Provider is one rate source: a public endpoint that gives USD and EUR in one answer.
type Provider struct {
	Name  string // shown on screen
	URL   string
	parse func(io.Reader) (answer, error)
}

// answer is what a provider said; updated is zero when it gave no time.
type answer struct {
	usd, eur float64
	updated  time.Time
}

// MaxAge is the oldest provider update accepted: an aggregator that stopped updating
// must not pass for a fresh rate and hide the fallback or the stale mark.
const MaxAge = time.Hour

// CoinGecko is a price aggregator.
var CoinGecko = Provider{"CoinGecko", "https://api.coingecko.com/api/v3/simple/price?ids=monero&vs_currencies=usd,eur&include_last_updated_at=true",
	func(r io.Reader) (answer, error) {
		var b struct {
			Monero struct {
				USD     float64 `json:"usd"`
				EUR     float64 `json:"eur"`
				Updated int64   `json:"last_updated_at"`
			} `json:"monero"`
		}
		err := json.NewDecoder(r).Decode(&b)
		a := answer{usd: b.Monero.USD, eur: b.Monero.EUR}
		if b.Monero.Updated > 0 {
			a.updated = time.Unix(b.Monero.Updated, 0)
		}
		return a, err
	}}

// CoinPaprika is a price aggregator, the official Monero GUI wallet's alternative to Kraken.
var CoinPaprika = Provider{"CoinPaprika", "https://api.coinpaprika.com/v1/tickers/xmr-monero?quotes=USD,EUR",
	func(r io.Reader) (answer, error) {
		var b struct {
			Updated time.Time `json:"last_updated"`
			Quotes  map[string]struct {
				Price float64 `json:"price"`
			} `json:"quotes"`
		}
		err := json.NewDecoder(r).Decode(&b)
		return answer{usd: b.Quotes["USD"].Price, eur: b.Quotes["EUR"].Price, updated: b.Updated}, err
	}}

// Kraken is the exchange the official Monero GUI wallet asks by default; its ticker
// gives the last trade ("c") and no update time.
var Kraken = Provider{"Kraken", "https://api.kraken.com/0/public/Ticker?pair=XMRUSD,XMREUR",
	func(r io.Reader) (answer, error) {
		var b struct {
			Error  []string `json:"error"`
			Result map[string]struct {
				C []string `json:"c"`
			} `json:"result"`
		}
		if err := json.NewDecoder(r).Decode(&b); err != nil {
			return answer{}, err
		}
		if len(b.Error) > 0 {
			return answer{}, errors.New(ansi.Plain(b.Error[0])) // shown on the terminal
		}
		last := func(pair string) float64 {
			if c := b.Result[pair].C; len(c) > 0 {
				v, _ := strconv.ParseFloat(c[0], 64)
				return v
			}
			return 0
		}
		return answer{usd: last("XXMRZUSD"), eur: last("XXMRZEUR")}, nil
	}}

// Providers are all asked at once and shown side by side; the order is the screen's.
var Providers = []Provider{CoinGecko, Kraken, CoinPaprika}

// Timeout bounds one request. Over a VPN on a mobile uplink CoinGecko often takes
// 3-5 s, so the node probes' short timeout is not used here.
const Timeout = 10 * time.Second

// Quote is the price of 1 XMR. Values are nil when the answer lacks them.
type Quote struct {
	USD, EUR *float64
	Source   string    // Provider.Name
	At       time.Time // when Moneroid received it
}

// Result is one provider's answer within FetchAll.
type Result struct {
	Quote *Quote
	Err   error
}

// FetchAll asks every provider at once, each within Timeout, and returns their
// answers in the order of ps.
func FetchAll(ctx context.Context, c *http.Client, ps []Provider, now func() time.Time) []Result {
	out := make([]Result, len(ps))
	var wg sync.WaitGroup
	for i, p := range ps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, Timeout)
			defer cancel()
			out[i].Quote, out[i].Err = Fetch(pctx, c, p, now())
		}()
	}
	wg.Wait()
	return out
}

// Median combines quotes per currency (the mean of the middle two for an even
// count), so one provider that is off cannot move the rate much; nil without quotes.
func Median(qs []*Quote) *Quote {
	var usd, eur []float64
	var at time.Time
	for _, q := range qs {
		if q == nil {
			continue
		}
		if q.USD != nil {
			usd = append(usd, *q.USD)
		}
		if q.EUR != nil {
			eur = append(eur, *q.EUR)
		}
		if q.At.After(at) {
			at = q.At
		}
	}
	if len(usd) == 0 && len(eur) == 0 {
		return nil
	}
	return &Quote{USD: median(usd), EUR: median(eur), At: at}
}

func median(v []float64) *float64 {
	if len(v) == 0 {
		return nil
	}
	sort.Float64s(v)
	m := v[len(v)/2]
	if len(v)%2 == 0 {
		m = (v[len(v)/2-1] + m) / 2
	}
	return &m
}

// Fetch asks p once.
func Fetch(ctx context.Context, c *http.Client, p Provider, now time.Time) (*Quote, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		var ue *url.Error // drop the URL Go repeats in every transport error
		if errors.As(err, &ue) {
			if ue.Timeout() {
				return nil, fmt.Errorf("%s: no answer in time", p.Name)
			}
			err = ue.Err
		}
		return nil, fmt.Errorf("%s: %w", p.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", p.Name, resp.StatusCode)
	}
	a, err := p.parse(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.Name, err)
	}
	if !a.updated.IsZero() && now.Sub(a.updated) > MaxAge {
		return nil, fmt.Errorf("%s: rate last updated %s ago", p.Name, now.Sub(a.updated).Round(time.Minute))
	}
	q := &Quote{USD: positive(a.usd), EUR: positive(a.eur), Source: p.Name, At: now}
	if q.USD == nil && q.EUR == nil {
		return nil, fmt.Errorf("%s: no XMR price in the answer", p.Name)
	}
	return q, nil
}

func positive(v float64) *float64 {
	if !(v > 0) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

// PerDay is the expected XMR a day for hashrate H/s at the network difficulty and
// block reward (atomic units): a block every difficulty/hashrate seconds on average.
// It is an expectation; P2Pool payouts scatter around it.
func PerDay(hashrate, difficulty float64, rewardAtomic int64) float64 {
	if !(hashrate > 0) || !(difficulty > 0) || rewardAtomic <= 0 {
		return 0
	}
	return hashrate * 86400 / difficulty * float64(rewardAtomic) / 1e12
}
