package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/Plasmoid77/moneroid/internal/config"
	"github.com/Plasmoid77/moneroid/internal/node"
)

func cmdNode(cfgPath string, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: moneroid node list [--json] | node select [--dry-run]")
		return exitUsage
	}
	cfg, code := loadConfig(cfgPath)
	if code != exitOK {
		return code
	}
	switch args[0] {
	case "list":
		return nodeList(cfg, args[1:])
	case "select":
		return nodeSelect(cfg, args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown node command %q\n", args[0])
	return exitUsage
}

func probeList(cfg *config.Config) ([]node.Result, int) {
	if cfg.P2Pool.NodesFile == "" {
		fmt.Fprintln(os.Stderr, "p2pool.nodes_file is not set in moneroid.toml")
		return nil, exitUsage
	}
	f, err := os.Open(cfg.P2Pool.NodesFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, fs.ErrPermission) {
			return nil, exitDenied
		}
		return nil, exitUsage
	}
	defer f.Close()
	cands, err := node.ParseList(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cfg.P2Pool.NodesFile, err)
		return nil, exitUsage
	}
	if len(cands) == 0 {
		fmt.Fprintf(os.Stderr, "%s: no candidates\n", cfg.P2Pool.NodesFile)
		return nil, exitCheck
	}
	// Probe the way P2Pool connects: through its socks5 proxy when the params-file sets one.
	// An unreadable params-file stops here: it may name a proxy that probing directly would bypass.
	var d node.Dialer = node.DirectDialer
	if cfg.P2Pool.ParamsFile != "" {
		data, err := os.ReadFile(cfg.P2Pool.ParamsFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "p2pool.params_file (needed for its socks5 proxy):", err)
			if errors.Is(err, fs.ErrPermission) {
				return nil, exitDenied
			}
			return nil, exitUsage
		}
		d = node.ReadParams(data).Dialer()
	}
	return node.ProbeAll(context.Background(), node.NewHTTPClient(d), d, cands), exitOK
}

func nodeList(cfg *config.Config, args []string) int {
	flags := flag.NewFlagSet("node list", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	asJSON := flags.Bool("json", false, "print results as JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return exitUsage
	}
	res, code := probeList(cfg)
	if code != exitOK {
		return code
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(map[string]any{"schema_version": 1, "probed_at": time.Now().UTC(), "nodes": res})
		return exitOK
	}
	printNodes(res)
	return exitOK
}

func printNodes(res []node.Result) {
	fmt.Printf("%-36s %-6s %-6s %-8s %-6s %-10s %s\n", "HOST", "RPC", "ZMQ", "LATENCY", "SYNC", "HEIGHT", "NOTE")
	// Addr is shown in --json; the table keeps the configured name.
	for _, r := range res {
		note := r.Error
		switch {
		case r.Usable():
			note = "usable"
		case note == "" && r.HeadersOK != nil && !*r.HeadersOK:
			note = "block headers unavailable"
		case note == "" && r.ZMQOpen != nil && !*r.ZMQOpen:
			note = "zmq port closed"
		case note == "":
			note = "not usable"
		}
		fmt.Printf("%-36s %-6d %-6d %-8s %-6s %-10s %s\n", r.Host, r.RPC, r.ZMQ, ms(r.LatencyMs), boolStr(r.Synchronized), i64(r.Height), note)
	}
}

func ms(v *int64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%d ms", *v)
}

func nodeSelect(cfg *config.Config, args []string) int {
	flags := flag.NewFlagSet("node select", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dry := flags.Bool("dry-run", false, "show the change without writing")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return exitUsage
	}
	if cfg.P2Pool.ParamsFile == "" {
		fmt.Fprintln(os.Stderr, "p2pool.params_file is not set in moneroid.toml")
		return exitUsage
	}
	res, code := probeList(cfg)
	if code != exitOK {
		return code
	}
	printNodes(res)
	if !res[0].Usable() {
		fmt.Fprintln(os.Stderr, "no usable node: none is synchronized with an open ZMQ port")
		return exitCheck
	}
	best := res[0].Candidate
	if res[0].Addr != "" {
		best.Host = res[0].Addr // the address that answered, not the DNS name (P2Pool takes the first DNS answer)
	}
	old, err := os.ReadFile(cfg.P2Pool.ParamsFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, fs.ErrPermission) {
			return exitDenied
		}
		return exitCheck
	}
	updated := node.Rewrite(old, best)
	fmt.Printf("\nselected %s (rpc %d, zmq %d)\n", best.Host, best.RPC, best.ZMQ)
	diff := node.Diff(old, updated)
	if len(diff) == 0 {
		fmt.Println(cfg.P2Pool.ParamsFile + " already points at this node")
		return exitOK
	}
	for _, l := range diff {
		fmt.Println(l)
	}
	if *dry {
		fmt.Println("dry run: nothing written")
		return exitOK
	}
	if err := node.WriteAtomic(cfg.P2Pool.ParamsFile, updated); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, fs.ErrPermission) {
			fmt.Fprintln(os.Stderr, "hint: run through sudo")
			return exitDenied
		}
		return exitCheck
	}
	fmt.Printf("written %s; apply with: moneroid restart p2pool\n", cfg.P2Pool.ParamsFile)
	return exitOK
}
