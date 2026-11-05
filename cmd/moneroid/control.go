package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/Plasmoid77/moneroid/internal/config"
	"github.com/Plasmoid77/moneroid/internal/guard"
	"github.com/Plasmoid77/moneroid/internal/systemd"
)

const (
	controlDeadline = 90 * time.Second
	verifyDeadline  = 3 * time.Second
	exitTimeout     = 4
)

// targetUnits resolves all|p2pool|xmrig (CLI-01); order is the systemd start order.
func targetUnits(cfg *config.Config, args []string) ([]string, error) {
	target := "all"
	switch len(args) {
	case 0:
	case 1:
		target = args[0]
	default:
		return nil, errors.New("expected at most one target: all|p2pool|xmrig")
	}
	switch target {
	case "all":
		return []string{cfg.Services.P2Pool, cfg.Services.XMRig}, nil
	case "p2pool":
		return []string{cfg.Services.P2Pool}, nil
	case "xmrig":
		return []string{cfg.Services.XMRig}, nil
	}
	return nil, fmt.Errorf("unknown target %q: use all, p2pool or xmrig", target)
}

func cmdControl(cfgPath, verb string, args []string) int {
	cfg, code := loadConfig(cfgPath)
	if code != exitOK {
		return code
	}
	units, err := targetUnits(cfg, args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	cleared, done, herr := guard.Manual(units, cfg.Services.XMRig)
	defer done()
	if cleared {
		fmt.Println("guard hold cleared: the guard will not start XMRig on its own")
	}
	if herr != nil {
		fmt.Fprintf(os.Stderr, "warning: guard hold: %v; run through sudo\n", herr)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cctx, cancel := context.WithTimeout(ctx, controlDeadline)
	if verb != "stop" {
		// A unit stuck in start-limit-hit refuses a plain start until reset-failed (CLI-04).
		sctx, scancel := context.WithTimeout(ctx, verifyDeadline)
		props, perr := systemd.Show(sctx, systemd.ExecRunner, units...)
		scancel()
		if perr == nil {
			for _, u := range units {
				if props[u]["Result"] == "start-limit-hit" {
					if rerr := systemd.Control(cctx, systemd.ExecRunner, "reset-failed", u); rerr != nil {
						fmt.Fprintln(os.Stderr, rerr)
					}
				}
			}
		}
	}
	err = systemd.Control(cctx, systemd.ExecRunner, verb, units...)
	cancel()

	result := exitOK
	switch {
	case err == nil:
		fmt.Printf("systemctl %s: done\n", verb)
	case ctx.Err() != nil:
		fmt.Fprintf(os.Stderr, "systemctl %s interrupted; the job handed to systemd may still complete — check `moneroid status`\n", verb)
		result = exitInterrupt
	case errors.Is(cctx.Err(), context.DeadlineExceeded):
		fmt.Fprintf(os.Stderr, "systemctl %s did not finish within %s; the job may still complete — check `moneroid status`\n", verb, controlDeadline)
		result = exitTimeout
	default:
		fmt.Fprintln(os.Stderr, err)
		var ce *systemd.ControlError
		if errors.As(err, &ce) && ce.Denied() {
			fmt.Fprintln(os.Stderr, "hint: run through sudo or grant the operator group a polkit rule for these units")
			result = exitDenied
		} else {
			result = exitCheck
		}
	}

	// CLI-05: always report the observed states afterwards.
	vctx, vcancel := context.WithTimeout(context.Background(), verifyDeadline)
	defer vcancel()
	props, perr := systemd.Show(vctx, systemd.ExecRunner, units...)
	if perr != nil {
		fmt.Fprintln(os.Stderr, "state check:", perr)
		return result
	}
	for _, u := range units {
		p := props[u]
		fmt.Printf("%-28s %s/%s", u, p["ActiveState"], p["SubState"])
		if p["LoadState"] != "loaded" {
			fmt.Printf(" (%s)", p["LoadState"])
		}
		if p["Result"] != "" && p["Result"] != "success" {
			fmt.Printf(" result=%s", p["Result"])
		}
		fmt.Println()
		want := "active"
		if verb == "stop" {
			want = "inactive"
		}
		if result == exitOK && p["ActiveState"] != want {
			result = exitCheck
		}
	}
	return result
}

func cmdLogs(cfgPath string, args []string) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	follow := fs.Bool("follow", false, "keep reading new entries")
	lines := fs.Int("lines", 100, "number of recent entries (1..10000)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *lines < 1 || *lines > 10000 {
		fmt.Fprintln(os.Stderr, "--lines must be within 1..10000")
		return exitUsage
	}
	cfg, code := loadConfig(cfgPath)
	if code != exitOK {
		return code
	}
	units, err := targetUnits(cfg, fs.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	cmd := exec.Command("journalctl", systemd.JournalArgs(units, *lines, *follow)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Ctrl-C is the normal way to leave --follow: journalctl gets the default
	// action, this process only observes the signal (SIG_IGN would be inherited).
	signal.Notify(make(chan os.Signal, 1), os.Interrupt)
	defer signal.Reset(os.Interrupt)
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if ee.ExitCode() == -1 || (*follow && ee.ExitCode() != 0) {
				return exitOK // terminated by a signal
			}
			return exitCheck
		}
		fmt.Fprintln(os.Stderr, "journalctl:", err)
		return exitCheck
	}
	return exitOK
}
