// Package config loads and validates moneroid.toml (spec §5.3, CFG-01..08).
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

const DefaultPath = "/etc/moneroid/moneroid.toml"

type Config struct {
	SchemaVersion int `toml:"schema_version"`
	Services      struct {
		P2Pool string `toml:"p2pool"`
		XMRig  string `toml:"xmrig"`
	} `toml:"services"`
	P2Pool struct {
		DataAPIDir string `toml:"data_api_dir"`
		ParamsFile string `toml:"params_file"`
		NodesFile  string `toml:"nodes_file"`
	} `toml:"p2pool"`
	XMRig struct {
		APIURL     string `toml:"api_url"`
		ExpectedID string `toml:"expected_id"`
		TokenFile  string `toml:"token_file"`
	} `toml:"xmrig"`
	UI struct {
		RefreshMs int  `toml:"refresh_ms"`
		Price     bool `toml:"price"` // TUI: XMR in USD/EUR from CoinGecko (PRICE-01); false = no request at all
	} `toml:"ui"`
	Guard Guard `toml:"guard"`
}

// Guard tunes `moneroid guard` (GUARD-01..08). Every key is optional.
type Guard struct {
	IntervalS       int  `toml:"interval_s"`
	HoldAfter       int  `toml:"hold_after"`
	ReleaseAfter    int  `toml:"release_after"`
	Repair          bool `toml:"repair"`
	RepairAfterS    int  `toml:"repair_after_s"`
	RepairCooldownS int  `toml:"repair_cooldown_s"`
}

var unitRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.@:-]*\.service$`)

// Load reads and validates the file at path. Missing file is an error (CFG-01).
func Load(path string) (*Config, error) {
	var c Config
	c.UI.RefreshMs, c.UI.Price = 1000, true
	c.Guard = Guard{IntervalS: 30, HoldAfter: 3, ReleaseAfter: 3, Repair: true, RepairAfterS: 1200, RepairCooldownS: 3600}
	md, err := toml.DecodeFile(path, &c)
	if err != nil {
		return nil, err
	}
	if u := md.Undecoded(); len(u) > 0 {
		return nil, fmt.Errorf("unknown key %q", u[0].String())
	}
	return &c, c.validate()
}

func (c *Config) validate() error {
	if c.SchemaVersion != 1 {
		return fmt.Errorf("schema_version must be 1, got %d", c.SchemaVersion)
	}
	for name, u := range map[string]string{"services.p2pool": c.Services.P2Pool, "services.xmrig": c.Services.XMRig} {
		if !unitRe.MatchString(u) {
			return fmt.Errorf("%s: invalid unit name %q", name, u)
		}
	}
	if c.Services.P2Pool == c.Services.XMRig {
		return errors.New("services.p2pool and services.xmrig must differ")
	}
	if err := absPath("p2pool.data_api_dir", c.P2Pool.DataAPIDir, true); err != nil {
		return err
	}
	for name, p := range map[string]string{"p2pool.params_file": c.P2Pool.ParamsFile, "p2pool.nodes_file": c.P2Pool.NodesFile, "xmrig.token_file": c.XMRig.TokenFile} {
		if err := absPath(name, p, false); err != nil {
			return err
		}
	}
	if err := checkAPIURL(c.XMRig.APIURL); err != nil {
		return fmt.Errorf("xmrig.api_url: %w", err)
	}
	if id := c.XMRig.ExpectedID; id == "" || len(id) > 128 || strings.ContainsFunc(id, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return errors.New("xmrig.expected_id must be 1..128 printable characters")
	}
	if r := c.UI.RefreshMs; r < 500 || r > 10000 {
		return fmt.Errorf("ui.refresh_ms must be within 500..10000, got %d", r)
	}
	g := c.Guard
	switch {
	case g.IntervalS < 10 || g.IntervalS > 600:
		return fmt.Errorf("guard.interval_s must be within 10..600, got %d", g.IntervalS)
	case g.HoldAfter < 1 || g.HoldAfter > 20, g.ReleaseAfter < 1 || g.ReleaseAfter > 20:
		return errors.New("guard.hold_after and guard.release_after must be within 1..20")
	case g.RepairAfterS < 300 || g.RepairAfterS > 86400:
		return fmt.Errorf("guard.repair_after_s must be within 300..86400, got %d", g.RepairAfterS)
	case g.RepairCooldownS < g.RepairAfterS || g.RepairCooldownS > 7*86400:
		return errors.New("guard.repair_cooldown_s must be within repair_after_s..604800")
	}
	return nil
}

func absPath(name, p string, required bool) error {
	if p == "" {
		if required {
			return fmt.Errorf("%s is required", name)
		}
		return nil
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("%s must be an absolute path", name)
	}
	return nil
}

// checkAPIURL enforces CFG-04: http, literal loopback IP, explicit port, nothing else.
func checkAPIURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("must be http://IP:PORT without path, query, fragment or credentials")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return errors.New("host must be a literal loopback IP")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return errors.New("explicit port 1..65535 required")
	}
	return nil
}

// ReadToken reads token_file per CFG-05. Empty TokenFile yields "".
func (c *Config) ReadToken() (string, error) {
	if c.XMRig.TokenFile == "" {
		return "", nil
	}
	b, err := os.ReadFile(c.XMRig.TokenFile)
	if err != nil {
		return "", err
	}
	if len(b) > 4096 {
		return "", errors.New("token file larger than 4 KiB")
	}
	t := strings.TrimRight(string(b), "\r\n")
	if strings.ContainsAny(t, "\r\n") {
		return "", errors.New("token contains line breaks")
	}
	return t, nil
}
