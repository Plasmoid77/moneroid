package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Plasmoid77/moneroid/internal/payouts"
)

func cmdPayouts(cfgPath string, args []string) int {
	flags := flag.NewFlagSet("payouts", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	asJSON := flags.Bool("json", false, "print as JSON")
	since := flags.String("since", "", "journalctl --since expression, e.g. \"-30 days\" or 2026-09-01")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return exitUsage
	}
	cfg, code := loadConfig(cfgPath)
	if code != exitOK {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rep, err := payouts.Fetch(ctx, cfg.Services.P2Pool, *since)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, payouts.ErrNoAccess) || errors.Is(err, os.ErrPermission) {
			return exitDenied
		}
		return exitCheck
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(rep)
		return exitOK
	}
	if len(rep.Payouts) == 0 {
		fmt.Printf("no payouts in the journal of %s", rep.Unit)
		if rep.BlocksWithout > 0 {
			fmt.Printf(" (%d pool blocks found while you had no share in the PPLNS window)", rep.BlocksWithout)
		}
		fmt.Println()
		return exitOK
	}
	fmt.Printf("%-25s %-18s %s\n", "TIME ("+time.Now().Format("MST")+")", "XMR", "BLOCK")
	for _, p := range rep.Payouts {
		at := "unknown"
		if !p.At.IsZero() {
			at = p.At.Local().Format("2006-01-02 15:04:05")
		}
		fmt.Printf("%-25s %-18s %d\n", at, p.XMR, p.Block)
	}
	fmt.Printf("\n%d payouts, total %s XMR", len(rep.Payouts), rep.TotalXMR)
	if rep.BlocksWithout > 0 {
		fmt.Printf("; %d pool blocks without a payout (no share in window)", rep.BlocksWithout)
	}
	fmt.Println()
	return exitOK
}
