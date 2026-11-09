// Package payouts lists P2Pool payout events from the unit journal (spec §6.2).
// This is the one place Moneroid reads log lines: payouts are events P2Pool
// reports nowhere else, not metrics.
package payouts

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// P2Pool 4.18, src/p2pool.cpp: "Your wallet <addr> got a payout of <int>.<12 digits> XMR in block <height>".
var (
	payoutRe   = regexp.MustCompile(`got a payout of (\d+)\.(\d{12}) XMR in block (\d+)`)
	noPayoutRe = regexp.MustCompile(`didn't get a payout in block (\d+)`)
)

const atomicPerXMR = 1_000_000_000_000

type Payout struct {
	At          time.Time `json:"at"`
	AtomicUnits uint64    `json:"atomic_units"`
	XMR         string    `json:"xmr"` // as logged, 12 decimals
	Block       uint64    `json:"block"`
}

type Report struct {
	SchemaVersion    int      `json:"schema_version"`
	Unit             string   `json:"unit"`
	Payouts          []Payout `json:"payouts"`
	TotalAtomicUnits uint64   `json:"total_atomic_units"`
	TotalXMR         string   `json:"total_xmr"`
	BlocksWithout    int      `json:"blocks_without_payout"` // pool blocks found while we had no share in the PPLNS window
}

// ErrNoAccess: the user may not read the system journal, so "no payouts" would be a lie (PAY-03).
var ErrNoAccess = errors.New("no access to the system journal: add this user to group systemd-journal (or adm) and log in again")

// Fetch asks journald for the matching lines only (-g is evaluated by journald) and
// parses them as they stream in, so a long journal history has no size limit.
// No -q: without it journalctl reports on stderr that the caller cannot see system messages.
func Fetch(ctx context.Context, unit, since string) (*Report, error) {
	cmd := exec.CommandContext(ctx, "journalctl", journalArgs(unit, since)...)
	cmd.Env = append(cmd.Environ(), "SYSTEMD_PAGER=", "SYSTEMD_COLORS=0", "LC_ALL=C")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("journalctl: %w", err)
	}
	rep, perr := Parse(unit, out)
	err = cmd.Wait() // before stderr is read: os/exec writes it until Wait returns
	if rep, err := finish(rep, stderr.String(), err); err != nil || perr == nil {
		return rep, err
	}
	return nil, fmt.Errorf("journalctl output: %w", perr)
}

func journalArgs(unit, since string) []string {
	args := []string{"--no-pager", "-o", "short-iso", "-u", unit, "-g", "got a payout of|didn't get a payout in block"}
	if since != "" {
		args = append(args, "--since", since)
	}
	return args
}

// finish classifies journalctl's exit (PAY-03).
func finish(rep *Report, stderr string, err error) (*Report, error) {
	msg := strings.TrimSpace(stderr)
	if strings.Contains(msg, "not seeing messages") {
		return nil, ErrNoAccess
	}
	if err != nil {
		// journalctl exits 1 with nothing on stderr when no entry matches -g.
		var ee *exec.ExitError
		if !(errors.As(err, &ee) && ee.ExitCode() == 1 && msg == "") {
			return nil, fmt.Errorf("journalctl: %w: %s", err, msg)
		}
	}
	return rep, nil
}

// Parse extracts payouts from `journalctl -o short-iso` output. Lines have no length
// limit (journald caps them itself); a read error is returned with what was parsed.
func Parse(unit string, in io.Reader) (*Report, error) {
	r := &Report{SchemaVersion: 1, Unit: unit, Payouts: []Payout{}}
	br := bufio.NewReader(in)
	var err error
	for err == nil {
		var line string
		line, err = br.ReadString('\n')
		if m := payoutRe.FindStringSubmatch(line); m != nil {
			whole, _ := strconv.ParseUint(m[1], 10, 64)
			frac, _ := strconv.ParseUint(m[2], 10, 64)
			block, _ := strconv.ParseUint(m[3], 10, 64)
			p := Payout{AtomicUnits: whole*atomicPerXMR + frac, XMR: m[1] + "." + m[2], Block: block}
			if ts, _, ok := strings.Cut(line, " "); ok && ts != "" {
				if t, err := time.Parse("2006-01-02T15:04:05-0700", ts); err == nil {
					p.At = t.UTC()
				} else if t, err := time.Parse("2006-01-02T15:04:05Z07:00", ts); err == nil {
					p.At = t.UTC()
				}
			}
			r.Payouts = append(r.Payouts, p)
			r.TotalAtomicUnits += p.AtomicUnits
		} else if noPayoutRe.MatchString(line) {
			r.BlocksWithout++
		}
	}
	r.TotalXMR = FormatXMR(r.TotalAtomicUnits)
	if err == io.EOF {
		err = nil
	}
	return r, err
}

// FormatXMR prints atomic units as P2Pool does: integer part, 12 decimals.
func FormatXMR(a uint64) string {
	return fmt.Sprintf("%d.%012d", a/atomicPerXMR, a%atomicPerXMR)
}
