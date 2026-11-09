// Package price reads the XMR exchange rate in USD and EUR for the TUI
// (spec PRICE-01..03). One public GET, no key, no address or other
// identifying data in the request; the rate is display-only.
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
	"time"
)

// URL is CoinGecko's public simple-price endpoint (one request gives both currencies).
const URL = "https://api.coingecko.com/api/v3/simple/price?ids=monero&vs_currencies=usd,eur"

// Source names the provider on screen.
const Source = "CoinGecko"

// Timeout bounds one request. Over a VPN on a mobile uplink CoinGecko often takes
// 3-5 s, so the node probes' short timeout is not used here.
const Timeout = 20 * time.Second

// Quote is the price of 1 XMR. Values are nil when the answer lacks them.
type Quote struct {
	USD, EUR *float64
	At       time.Time // when Moneroid received it
}

// Fetch asks endpoint (normally URL) once.
func Fetch(ctx context.Context, c *http.Client, endpoint string, now time.Time) (*Quote, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		var ue *url.Error // drop the URL Go repeats in every transport error
		if errors.As(err, &ue) {
			if ue.Timeout() {
				return nil, fmt.Errorf("%s: no answer in time", Source)
			}
			err = ue.Err
		}
		return nil, fmt.Errorf("%s: %w", Source, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", Source, resp.StatusCode)
	}
	var body struct {
		Monero map[string]float64 `json:"monero"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return nil, fmt.Errorf("%s: %w", Source, err)
	}
	q := &Quote{USD: positive(body.Monero["usd"]), EUR: positive(body.Monero["eur"]), At: now}
	if q.USD == nil && q.EUR == nil {
		return nil, fmt.Errorf("%s: no XMR price in the answer", Source)
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
