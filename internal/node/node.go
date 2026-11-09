// Package node probes candidate Monero nodes and rewrites the node keys of the
// P2Pool params-file (spec §6.1, NODE-01..05).
package node

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Plasmoid77/moneroid/internal/ansi"
	"github.com/Plasmoid77/moneroid/internal/jsonx"
)

const (
	ProbeTimeout = 3 * time.Second
	maxBody      = 1 << 20
)

type Candidate struct {
	Host string `json:"host"`
	RPC  int    `json:"rpc_port"`
	ZMQ  int    `json:"zmq_port"`
}

// Result.Addr is the IP literal that answered; select writes it instead of the
// hostname because P2Pool uses the first DNS answer, which may be an IPv6
// address the host cannot reach (Zeonux, 2026-09-14).

// ParseList parses nodes.txt: `host rpc_port zmq_port` per line, `#` comments (NODE-01).
func ParseList(r io.Reader) ([]Candidate, error) {
	var out []Candidate
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 {
			return nil, fmt.Errorf("line %d: expected `host rpc_port zmq_port`", n)
		}
		rpc, err1 := strconv.Atoi(f[1])
		zmq, err2 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil || rpc < 1 || rpc > 65535 || zmq < 1 || zmq > 65535 || strings.ContainsAny(f[0], "/@") || (strings.Contains(f[0], ":") && net.ParseIP(f[0]) == nil) {
			return nil, fmt.Errorf("line %d: invalid host or ports", n)
		}
		out = append(out, Candidate{Host: f[0], RPC: rpc, ZMQ: zmq})
	}
	return out, sc.Err()
}

// Result is the outcome of probing one candidate.
type Result struct {
	Candidate
	LatencyMs    *int64 `json:"latency_ms"`
	Synchronized *bool  `json:"synchronized"`
	Height       *int64 `json:"height"`
	Addr         string `json:"addr,omitempty"`
	HeadersOK    *bool  `json:"headers_ok"` // get_block_headers_range of 100 blocks succeeded
	ZMQOpen      *bool  `json:"zmq_open"`
	Error        string `json:"error,omitempty"`
}

// Usable reports whether the node is synchronized, serves block header ranges
// (what P2Pool downloads at start) and has an open ZMQ port.
func (r Result) Usable() bool {
	return r.Synchronized != nil && *r.Synchronized && r.HeadersOK != nil && *r.HeadersOK && r.ZMQOpen != nil && *r.ZMQOpen
}

// rpc posts one JSON-RPC call to addr:port with its own ProbeTimeout and returns the decoded body.
func rpc(ctx context.Context, client *http.Client, addr string, port int, method string, params string) (jsonx.Object, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	body := strings.NewReader(`{"jsonrpc":"2.0","id":"0","method":"` + method + `","params":` + params + `}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://%s/json_rpc", net.JoinHostPort(addr, strconv.Itoa(port))), body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	o, err := jsonx.Decode(raw)
	if err != nil {
		return nil, 0, errors.New("invalid JSON-RPC response")
	}
	if e, ok := o["error"].(map[string]any); ok {
		// The node's own words end up on a terminal: no control characters, bounded length.
		msg := []rune(ansi.Plain(fmt.Sprint(e["message"])))
		if len(msg) > 200 {
			msg = append(msg[:200], '…')
		}
		return nil, 0, fmt.Errorf("%s: %s", method, string(msg))
	}
	return o, time.Since(start), nil
}

// Probe performs get_info, a 100-block get_block_headers_range (the call P2Pool
// makes at start, which some filtered networks break) and a ZMTP handshake with
// the ZMQ port. d is the dialer for the ZMQ check (nil = direct); the HTTP client
// carries its own.
func Probe(ctx context.Context, client *http.Client, d Dialer, c Candidate) Result {
	// Every resolved address gets the full check; the first fully usable one wins,
	// otherwise the first that answered get_info is reported (Codex review 2026-09-16).
	var first *Result
	for _, addr := range resolve(ctx, c.Host) {
		r := probeAddr(ctx, client, d, c, addr)
		if r.Usable() {
			return r
		}
		if first == nil || (first.LatencyMs == nil && r.LatencyMs != nil) {
			first = &r
		}
	}
	if first == nil {
		return Result{Candidate: c, Error: "no address"}
	}
	return *first
}

// Ping is one get_info round trip to the first address of c that answers:
// latency, height and sync state without the heavier checks of Probe (the TUI
// asks every minute, possibly over a metered uplink).
func Ping(ctx context.Context, client *http.Client, c Candidate) Result {
	res := Result{Candidate: c, Error: "no address"}
	for _, addr := range resolve(ctx, c.Host) {
		if res = getInfo(ctx, client, c, addr); res.Error == "" {
			break
		}
	}
	return res
}

func getInfo(ctx context.Context, client *http.Client, c Candidate, addr string) Result {
	res := Result{Candidate: c, Addr: addr}
	o, took, err := rpc(ctx, client, addr, c.RPC, "get_info", "{}")
	if err != nil {
		res.Error = err.Error()
		return res
	}
	lat := took.Milliseconds()
	res.LatencyMs = &lat
	if v, fe := o.Int("result", "height"); fe == nil {
		res.Height = &v
	}
	if r, ok := o["result"].(map[string]any); ok {
		if b, ok := r["synchronized"].(bool); ok {
			res.Synchronized = &b
		}
	}
	return res
}

func probeAddr(ctx context.Context, client *http.Client, d Dialer, c Candidate, addr string) Result {
	res := getInfo(ctx, client, c, addr)
	if res.Error != "" {
		return res
	}
	if res.Height != nil && *res.Height > 100 {
		h, _, err := rpc(ctx, client, res.Addr, c.RPC, "get_block_headers_range", fmt.Sprintf(`{"start_height":%d,"end_height":%d}`, *res.Height-101, *res.Height-1))
		ok := err == nil
		if ok {
			_, fe := h.Index(0, "result", "headers")
			ok = fe == nil
		}
		res.HeadersOK = &ok
		if !ok && err != nil {
			res.Error = "headers: " + err.Error()
		}
	}
	open := zmtpHandshake(ctx, d, res.Addr, c.ZMQ)
	res.ZMQOpen = &open
	return res
}

// zmtpHandshake connects to the ZMQ port and exchanges the ZMTP greeting
// signature (0xff, 8 bytes, 0x7f): an accepting TCP port is not proof of a
// ZMQ publisher (xmr.privacy.cash, 2026-09-14).
func zmtpHandshake(ctx context.Context, d Dialer, addr string, port int) bool {
	if d == nil {
		d = DirectDialer
	}
	zctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	conn, err := d(zctx, "tcp", net.JoinHostPort(addr, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(ProbeTimeout))
	if _, err := conn.Write([]byte{0xff, 0, 0, 0, 0, 0, 0, 0, 1, 0x7f}); err != nil {
		return false
	}
	buf := make([]byte, 10)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return false
	}
	return buf[0] == 0xff && buf[9] == 0x7f
}

// resolve returns the host's addresses, IPv4 first; a literal is returned as is.
func resolve(ctx context.Context, host string) []string {
	if ip := net.ParseIP(host); ip != nil {
		return []string{host}
	}
	rctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(rctx, host)
	if err != nil || len(ips) == 0 {
		return []string{host}
	}
	var v4, v6 []string
	for _, ip := range ips {
		if ip.IP.To4() != nil {
			v4 = append(v4, ip.IP.String())
		} else {
			v6 = append(v6, ip.IP.String())
		}
	}
	return append(v4, v6...)
}

// probeParallel bounds concurrent probes: a long nodes.txt probed all at once
// through a VPN or SOCKS proxy gives false timeouts and inflated latencies.
const probeParallel = 4

// ProbeAll probes candidates, probeParallel at a time, and returns them ranked
// (NODE-02): usable nodes first by latency, then the rest in input order.
func ProbeAll(ctx context.Context, client *http.Client, d Dialer, cands []Candidate) []Result {
	results := make([]Result, len(cands))
	var wg sync.WaitGroup
	sem := make(chan struct{}, probeParallel)
	for i, c := range cands {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			results[i] = Probe(ctx, client, d, c)
		}()
	}
	wg.Wait()
	sort.SliceStable(results, func(a, b int) bool {
		ua, ub := results[a].Usable(), results[b].Usable()
		if ua != ub {
			return ua
		}
		return ua && *results[a].LatencyMs < *results[b].LatencyMs
	})
	return results
}

// Dialer opens TCP connections; the default is a plain net.Dialer, the
// alternative is SOCKS5 so probes take the same path as P2Pool's `socks5`.
type Dialer func(ctx context.Context, network, addr string) (net.Conn, error)

// DirectDialer dials without a proxy.
func DirectDialer(ctx context.Context, network, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: ProbeTimeout}
	return d.DialContext(ctx, network, addr)
}

// SOCKS5Dialer returns a Dialer that issues a SOCKS5 CONNECT (no auth) through proxy.
func SOCKS5Dialer(proxy string) Dialer {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, err
		}
		conn, err := DirectDialer(ctx, "tcp", proxy)
		if err != nil {
			return nil, fmt.Errorf("socks5 %s: %w", proxy, err)
		}
		if dl, ok := ctx.Deadline(); ok {
			conn.SetDeadline(dl)
		}
		req := []byte{5, 1, 0} // version 5, one method: no auth
		if _, err = conn.Write(req); err == nil {
			buf := make([]byte, 2)
			if _, err = io.ReadFull(conn, buf); err == nil && (buf[0] != 5 || buf[1] != 0) {
				err = errors.New("socks5: no-auth method rejected")
			}
		}
		if err != nil {
			conn.Close()
			return nil, err
		}
		req = []byte{5, 1, 0}
		if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
			req = append(append(req, 1), ip.To4()...)
		} else if ip != nil {
			req = append(append(req, 4), ip.To16()...)
		} else if len(host) <= 255 {
			req = append(append(req, 3, byte(len(host))), host...)
		} else {
			conn.Close()
			return nil, errors.New("socks5: host name longer than 255 bytes")
		}
		req = append(req, byte(port>>8), byte(port))
		if _, err = conn.Write(req); err == nil {
			head := make([]byte, 4)
			if _, err = io.ReadFull(conn, head); err == nil {
				if head[1] != 0 {
					err = fmt.Errorf("socks5: connect refused (reply %d)", head[1])
				} else {
					skip := map[byte]int{1: 4, 4: 16}[head[3]]
					if head[3] == 3 {
						l := make([]byte, 1)
						_, err = io.ReadFull(conn, l)
						skip = int(l[0])
					}
					if err == nil {
						_, err = io.ReadFull(conn, make([]byte, skip+2))
					}
				}
			}
		}
		if err != nil {
			conn.Close()
			return nil, err
		}
		conn.SetDeadline(time.Time{})
		return conn, nil
	}
}

// NewHTTPClient returns a client without env proxy or redirects, dialing through d.
func NewHTTPClient(d Dialer) *http.Client {
	if d == nil {
		d = DirectDialer
	}
	return &http.Client{
		Timeout:       ProbeTimeout,
		Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: d},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirects are not followed") },
	}
}

var nodeKeys = []string{"host", "rpc-port", "zmq-port"}

// Params is the subset of the params-file Moneroid reads.
type Params struct {
	Node    Candidate
	Socks5  string // `socks5 = IP:port`, empty when P2Pool connects directly
	DataDir string // `data-dir`, empty when P2Pool uses its working directory
}

// Dialer returns the dialer matching how P2Pool connects.
// Like P2Pool (is_private_address), loopback/private/link-local targets bypass
// the proxy, so a LAN node keeps working in Tor/I2P setups.
func (p Params) Dialer() Dialer {
	if p.Socks5 == "" {
		return DirectDialer
	}
	viaProxy := SOCKS5Dialer(p.Socks5)
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if host, _, err := net.SplitHostPort(addr); err == nil {
			if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
				return DirectDialer(ctx, network, addr)
			}
		}
		return viaProxy(ctx, network, addr)
	}
}

// ReadParams returns the node keys of a params-file, with P2Pool defaults for absent keys.
func ReadParams(data []byte) Params {
	p := Params{Node: Candidate{Host: "127.0.0.1", RPC: 18081, ZMQ: 18083}}
	c := &p.Node
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := splitKV(line)
		if !ok {
			continue
		}
		switch k {
		case "host":
			c.Host = v
		case "rpc-port":
			if n, err := strconv.Atoi(v); err == nil {
				c.RPC = n
			}
		case "zmq-port":
			if n, err := strconv.Atoi(v); err == nil {
				c.ZMQ = n
			}
		case "socks5":
			p.Socks5 = v
		case "data-dir":
			p.DataDir = v
		}
	}
	return p
}

func splitKV(line string) (string, string, bool) {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", "", false
	}
	k, v, ok := strings.Cut(s, "=")
	if !ok {
		return "", "", false
	}
	v = strings.TrimSpace(v)
	if i := strings.IndexByte(v, '#'); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return strings.TrimSpace(k), strings.Trim(v, `"`), true
}

// Rewrite replaces exactly the three node keys, keeping every other byte (NODE-03).
// Missing keys are appended at the end; repeated node keys (P2Pool treats a
// second host line as a failover host) are dropped so the switch is complete.
func Rewrite(data []byte, c Candidate) []byte {
	values := map[string]string{"host": c.Host, "rpc-port": strconv.Itoa(c.RPC), "zmq-port": strconv.Itoa(c.ZMQ)}
	seen := map[string]bool{}
	lines := strings.SplitAfter(string(data), "\n")
	var out strings.Builder
	for _, line := range lines {
		k, _, ok := splitKV(line)
		if ok && values[k] != "" {
			if seen[k] {
				continue
			}
			seen[k] = true
			out.WriteString(k + " = " + values[k])
			if strings.HasSuffix(line, "\n") {
				out.WriteString("\n")
			}
			continue
		}
		out.WriteString(line)
	}
	s := out.String()
	for _, k := range nodeKeys {
		if !seen[k] {
			if s != "" && !strings.HasSuffix(s, "\n") {
				s += "\n"
			}
			s += k + " = " + values[k] + "\n"
		}
	}
	return []byte(s)
}

// WriteAtomic writes data next to path and renames it over path, preserving
// the original owner and mode.
func WriteAtomic(path string, data []byte) error {
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".p2pool.conf.*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		if err := tmp.Chown(int(sys.Uid), int(sys.Gid)); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := tmp.Chmod(st.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Diff lists the node-key lines that Rewrite removes and adds, for the output of
// `node select`. Only host/rpc-port/zmq-port lines are ever shown: the params-file
// also holds the wallet, which must not reach the terminal (SEC-05).
func Diff(old, new []byte) []string {
	o, n := nodeLines(old), nodeLines(new)
	var out []string
	for _, l := range o {
		if !contains(n, l) {
			out = append(out, "- "+l)
		}
	}
	for _, l := range n {
		if !contains(o, l) {
			out = append(out, "+ "+l)
		}
	}
	return out
}

func nodeLines(data []byte) []string {
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if k, _, ok := splitKV(line); ok && contains(nodeKeys, k) {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
