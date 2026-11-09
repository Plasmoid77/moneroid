// Package systemd reads unit state through systemctl (spec SYS-06, SYS-07).
package systemd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Runner executes a command with an argument vector; no shell is involved.
type Runner func(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)

// ExecRunner runs real commands with a bounded output size.
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(cmd.Environ(), "SYSTEMD_PAGER=", "SYSTEMD_COLORS=0", "LC_ALL=C")
	var out, errb bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &out, n: 1 << 20}
	cmd.Stderr = &limitedWriter{w: &errb, n: 64 << 10}
	err := cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

type limitedWriter struct {
	w *bytes.Buffer
	n int
}

// Write keeps the first n bytes and discards the rest. It always reports the full
// length: a short write would make os/exec fail the whole command.
func (l *limitedWriter) Write(p []byte) (int, error) {
	k := min(len(p), l.n)
	l.w.Write(p[:k])
	l.n -= k
	return len(p), nil
}

// Props holds the key=value output of `systemctl show` for one unit.
type Props map[string]string

// ShowProperties is the SYS-06 property list.
var ShowProperties = []string{
	"Id", "LoadState", "ActiveState", "SubState", "UnitFileState", "Result", "MainPID", "ExecMainStatus",
	"NRestarts", "InvocationID", "ActiveEnterTimestampMonotonic", "InactiveExitTimestampMonotonic",
	"ExecMainStartTimestamp", "ExecMainStartTimestampMonotonic",
	"After", "Wants", "Requires", "BindsTo", "PartOf", "RuntimeDirectory", "RuntimeDirectoryPreserve",
}

// Show returns properties per unit, keyed by the requested unit name.
// A missing unit is reported by systemd as LoadState=not-found, not as an error.
func Show(ctx context.Context, run Runner, units ...string) (map[string]Props, error) {
	args := append([]string{"show", "--no-pager", "--all", "--property=" + strings.Join(ShowProperties, ",")}, "--")
	args = append(args, units...)
	out, stderr, err := run(ctx, "systemctl", args...)
	if err != nil {
		return nil, fmt.Errorf("systemctl show: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	blocks := ParseShow(out)
	res := make(map[string]Props, len(units))
	for i, u := range units {
		if i < len(blocks) {
			res[u] = blocks[i]
		}
	}
	if len(res) != len(units) {
		return res, errors.New("systemctl show: fewer blocks than units")
	}
	return res, nil
}

// ParseShow splits `systemctl show` output into per-unit blocks (blank-line separated).
func ParseShow(out []byte) []Props {
	var blocks []Props
	cur := Props{}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			if len(cur) > 0 {
				blocks = append(blocks, cur)
				cur = Props{}
			}
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			cur[k] = v
		}
	}
	if len(cur) > 0 {
		blocks = append(blocks, cur)
	}
	return blocks
}

// HasDependency reports whether the space-separated list property contains unit.
func (p Props) HasDependency(prop, unit string) bool {
	for _, f := range strings.Fields(p[prop]) {
		if f == unit {
			return true
		}
	}
	return false
}

// Control runs `systemctl VERB -- UNIT...` (SYS-07). It returns systemd's stderr
// text on failure so the caller can classify the error.
func Control(ctx context.Context, run Runner, verb string, units ...string) error {
	args := append([]string{"--no-pager", "--no-ask-password", verb, "--"}, units...)
	_, stderr, err := run(ctx, "systemctl", args...)
	if err != nil {
		return &ControlError{Verb: verb, Err: err, Stderr: strings.TrimSpace(string(stderr))}
	}
	return nil
}

// ResetStartLimit runs reset-failed for the units stuck in start-limit-hit, which
// otherwise refuse a plain start (CLI-04). Best effort: unreadable states skip it.
func ResetStartLimit(ctx context.Context, run Runner, units ...string) error {
	props, err := Show(ctx, run, units...)
	if err != nil {
		return nil
	}
	var errs []error
	for _, u := range units {
		if props[u]["Result"] == "start-limit-hit" {
			errs = append(errs, Control(ctx, run, "reset-failed", u))
		}
	}
	return errors.Join(errs...)
}

// ControlError wraps a failed systemctl call.
type ControlError struct {
	Verb   string
	Err    error
	Stderr string
}

func (e *ControlError) Error() string {
	if e.Stderr != "" {
		return "systemctl " + e.Verb + ": " + e.Stderr
	}
	return "systemctl " + e.Verb + ": " + e.Err.Error()
}

func (e *ControlError) Unwrap() error { return e.Err }

// Denied reports whether systemd refused the operation for lack of privileges.
func (e *ControlError) Denied() bool {
	s := strings.ToLower(e.Stderr)
	return strings.Contains(s, "access denied") || strings.Contains(s, "interactive authentication required") || strings.Contains(s, "permission denied")
}

// JournalArgs builds the journalctl argument vector for `logs` (CLI-06).
func JournalArgs(units []string, lines int, follow bool) []string {
	args := []string{"--no-pager", "-o", "short-iso", "-n", strconv.Itoa(lines)}
	if follow {
		args = append(args, "-f")
	}
	for _, u := range units {
		args = append(args, "-u", u)
	}
	return args
}
