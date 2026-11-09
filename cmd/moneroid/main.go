// Command moneroid operates one P2Pool + XMRig pair under systemd (spec §6).
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

	"github.com/charmbracelet/colorprofile"

	"github.com/Plasmoid77/moneroid/internal/ansi"
	"github.com/Plasmoid77/moneroid/internal/config"
	"github.com/Plasmoid77/moneroid/internal/status"
	"github.com/Plasmoid77/moneroid/internal/systemd"
	"github.com/Plasmoid77/moneroid/internal/xmrig"
)

var version = "dev"

const usage = `Usage:
  moneroid [--config PATH] status [--json] [--check]
  moneroid [--config PATH] tui
  moneroid [--config PATH] start   [all|p2pool|xmrig]
  moneroid [--config PATH] stop    [all|p2pool|xmrig]
  moneroid [--config PATH] restart [all|p2pool|xmrig]
  moneroid [--config PATH] logs [--follow] [--lines N] [all|p2pool|xmrig]
  moneroid [--config PATH] doctor [--json]
  moneroid [--config PATH] payouts [--json] [--since TIME]
  moneroid [--config PATH] node list [--json]
  moneroid [--config PATH] node select [--dry-run]
  moneroid [--config PATH] config path
  moneroid [--config PATH] guard        (run by moneroid-guard.service)
  moneroid version
  moneroid --help

Examples:
  moneroid status
  moneroid --config /etc/moneroid/moneroid.toml status --json
  moneroid status --check && echo mining is healthy
  sudo moneroid restart xmrig
  moneroid logs --follow p2pool
`

// Exit codes (CLI-07).
const (
	exitOK        = 0
	exitCheck     = 1
	exitUsage     = 2
	exitDenied    = 3
	exitInterrupt = 130
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	global := flag.NewFlagSet("moneroid", flag.ContinueOnError)
	global.SetOutput(os.Stderr)
	global.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	cfgPath := global.String("config", config.DefaultPath, "path to moneroid.toml")
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	rest := global.Args()
	if len(rest) == 0 {
		fmt.Print(usage)
		return exitOK
	}
	switch rest[0] {
	case "version":
		fmt.Println("moneroid", version)
		return exitOK
	case "config":
		if len(rest) == 2 && rest[1] == "path" {
			fmt.Println(*cfgPath)
			return exitOK
		}
		fmt.Fprintln(os.Stderr, "usage: moneroid config path")
		return exitUsage
	case "status":
		return cmdStatus(*cfgPath, rest[1:])
	case "start", "stop", "restart":
		return cmdControl(*cfgPath, rest[0], rest[1:])
	case "logs":
		return cmdLogs(*cfgPath, rest[1:])
	case "doctor":
		return cmdDoctor(*cfgPath, rest[1:])
	case "node":
		return cmdNode(*cfgPath, rest[1:])
	case "payouts":
		return cmdPayouts(*cfgPath, rest[1:])
	case "tui":
		return cmdTUI(*cfgPath, rest[1:])
	case "guard":
		return cmdGuard(*cfgPath, rest[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown command %q\n%s", rest[0], usage)
	return exitUsage
}

func loadConfig(path string) (*config.Config, int) {
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config %s: %v\n", path, err)
		if errors.Is(err, fs.ErrPermission) {
			return nil, exitDenied
		}
		return nil, exitUsage
	}
	return cfg, exitOK
}

func cmdStatus(cfgPath string, args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "print the snapshot as JSON")
	check := fs.Bool("check", false, "exit 1 unless health.level is ok")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "status takes no positional arguments")
		return exitUsage
	}
	cfg, code := loadConfig(cfgPath)
	if code != exitOK {
		return code
	}
	token, err := cfg.ReadToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: token_file: %v\n", err)
	}
	c := &status.Collector{
		Cfg:       cfg,
		Run:       systemd.ExecRunner,
		XMRig:     &xmrig.Client{HTTP: xmrig.NewHTTPClient(status.ReadTimeout), BaseURL: cfg.XMRig.APIURL, Token: token},
		Now:       time.Now,
		Monotonic: status.Monotonic,
		HeldFile:  status.GuardHeldFile,
	}
	snap := c.Collect(context.Background())
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(snap); err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			return exitCheck
		}
	} else {
		printStatus(ansi.ConsoleWriter(colorprofile.NewWriter(os.Stdout, os.Environ())), snap) // strips colour for pipes and NO_COLOR
	}
	if *check && snap.Health.Level != status.LevelOK {
		return exitCheck
	}
	return exitOK
}
