package node

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseList(t *testing.T) {
	c, err := ParseList(strings.NewReader("# comment\n\nxmr.support 18081 18083 # trailing\n1.2.3.4 18089 18083\n"))
	if err != nil || len(c) != 2 || c[0].Host != "xmr.support" || c[1].RPC != 18089 {
		t.Fatalf("%v %v", c, err)
	}
	if c, err := ParseList(strings.NewReader("2001:db8::1 18081 18083\n")); err != nil || c[0].Host != "2001:db8::1" {
		t.Fatalf("IPv6 literal: %v %v", c, err)
	}
	for _, bad := range []string{"host 1", "host abc 18083", "http://x 18081 18083", "h 0 1", "a:b 18081 18083"} {
		if _, err := ParseList(strings.NewReader(bad)); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "get_block_headers_range") {
			w.Write([]byte(`{"jsonrpc":"2.0","id":"0","result":{"headers":[{"height":1}]}}`))
			return
		}
		w.Write([]byte(`{"jsonrpc":"2.0","id":"0","result":{"height":1000,"synchronized":true}}`))
	}))
	defer srv.Close()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() { // minimal ZMTP responder
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 10)
				io.ReadFull(c, buf)
				c.Write([]byte{0xff, 0, 0, 0, 0, 0, 0, 0, 1, 0x7f})
			}(c)
		}
	}()
	host, rpcs, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	rpc, _ := strconv.Atoi(rpcs)
	zmq := ln.Addr().(*net.TCPAddr).Port
	res := ProbeAll(context.Background(), NewHTTPClient(nil), nil, []Candidate{{Host: host, RPC: rpc, ZMQ: 1}, {Host: host, RPC: rpc, ZMQ: zmq}, {Host: host, RPC: 1, ZMQ: zmq}})
	if !res[0].Usable() || res[0].ZMQ != zmq || *res[0].Height != 1000 || !*res[0].HeadersOK {
		t.Fatalf("first %+v", res[0])
	}
	if res[1].Usable() || res[2].Error == "" {
		t.Fatalf("rest %+v %+v", res[1], res[2])
	}
	if p := Ping(context.Background(), NewHTTPClient(nil), Candidate{Host: host, RPC: rpc, ZMQ: 1}); p.Error != "" || p.LatencyMs == nil || *p.Height != 1000 || !*p.Synchronized || p.ZMQOpen != nil {
		t.Fatalf("ping %+v", p)
	}
	if p := Ping(context.Background(), NewHTTPClient(nil), Candidate{Host: host, RPC: 1}); p.Error == "" || p.LatencyMs != nil {
		t.Fatalf("ping of a closed port %+v", p)
	}
}

func TestRewrite(t *testing.T) {
	in := "# node\nhost = old.example   # keep comment? no, line is replaced\nwallet = 4abc\n\nrpc-port=18081\nmini = 1"
	out := string(Rewrite([]byte(in), Candidate{Host: "new.example", RPC: 18089, ZMQ: 18083}))
	want := "# node\nhost = new.example\nwallet = 4abc\n\nrpc-port = 18089\nmini = 1\nzmq-port = 18083\n"
	if out != want {
		t.Fatalf("got %q\nwant %q", out, want)
	}
	if p := ReadParams([]byte(out + "socks5 = 127.0.0.1:1080\n")); p.Node.Host != "new.example" || p.Node.RPC != 18089 || p.Node.ZMQ != 18083 || p.Socks5 != "127.0.0.1:1080" {
		t.Fatalf("read back %+v", p)
	}
	if p := ReadParams([]byte("data-dir = /var/lib/moneroid/p2pool # cache here\n")); p.DataDir != "/var/lib/moneroid/p2pool" {
		t.Fatalf("data-dir %q", p.DataDir)
	}
	if p := ReadParams(nil); p.Node.Host != "127.0.0.1" || p.Node.RPC != 18081 || p.Socks5 != "" || p.DataDir != "" {
		t.Fatalf("defaults %+v", p)
	}
	if d := Diff([]byte(in), []byte(out)); len(d) != 5 {
		t.Fatalf("diff %v", d)
	}
	dup := "host = a\nhost = b\nrpc-port = 1\nzmq-port = 2\nrpc-port = 3\n"
	if got := string(Rewrite([]byte(dup), Candidate{Host: "c", RPC: 4, ZMQ: 5})); got != "host = c\nrpc-port = 4\nzmq-port = 5\n" {
		t.Fatalf("duplicates must be dropped, got %q", got)
	}
}

func TestWriteAtomic(t *testing.T) {
	p := filepath.Join(t.TempDir(), "p2pool.conf")
	os.WriteFile(p, []byte("a = 1\n"), 0o640)
	if err := WriteAtomic(p, []byte("a = 2\n")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	st, _ := os.Stat(p)
	if string(b) != "a = 2\n" || st.Mode().Perm() != 0o640 {
		t.Fatalf("%q %v", b, st.Mode())
	}
	if err := WriteAtomic(filepath.Join(t.TempDir(), "missing"), nil); err == nil {
		t.Fatal("missing file must not be created")
	}
}

// TestSOCKS5Dialer runs a minimal SOCKS5 server that forwards to the test HTTP server.
func TestSOCKS5Dialer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"jsonrpc":"2.0","id":"0","result":{"height":1000,"synchronized":true,"headers":[{"height":1}]}}`))
	}))
	defer srv.Close()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 3)
				io.ReadFull(c, buf)
				c.Write([]byte{5, 0})
				head := make([]byte, 4)
				io.ReadFull(c, head)
				var target string
				switch head[3] {
				case 1:
					b := make([]byte, 6)
					io.ReadFull(c, b)
					target = net.JoinHostPort(net.IP(b[:4]).String(), strconv.Itoa(int(b[4])<<8|int(b[5])))
				default:
					c.Write([]byte{5, 8, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				up, err := net.Dial("tcp", target)
				if err != nil {
					c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				defer up.Close()
				c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				go io.Copy(up, c)
				io.Copy(c, up)
			}(c)
		}
	}()
	host, rpcs, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	rpc, _ := strconv.Atoi(rpcs)
	d := SOCKS5Dialer(ln.Addr().String())
	res := Probe(context.Background(), NewHTTPClient(d), d, Candidate{Host: host, RPC: rpc, ZMQ: 1})
	if res.LatencyMs == nil || res.HeadersOK == nil || !*res.HeadersOK {
		t.Fatalf("probe through socks5: %+v", res)
	}
	if res.ZMQOpen == nil || *res.ZMQOpen {
		t.Fatalf("zmq on port 1 must be closed through socks5: %+v", res)
	}
	if _, err := SOCKS5Dialer("127.0.0.1:1")(context.Background(), "tcp", "1.2.3.4:80"); err == nil {
		t.Fatal("dead proxy must fail")
	}
}

func TestParamsDialerBypassesProxyForPrivateAddresses(t *testing.T) {
	// A listener stands in for a LAN node; the proxy address is a closed port,
	// so a dial that went through the proxy would fail.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	d := Params{Socks5: "127.0.0.1:1"}.Dialer()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := d(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("private address must bypass the proxy: %v", err)
	}
	c.Close()
	if _, err := d(ctx, "tcp", "203.0.113.1:18081"); err == nil || !strings.Contains(err.Error(), "socks5") {
		t.Fatalf("public address must go through the proxy, got %v", err)
	}
}
