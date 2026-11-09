package payouts

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const journal = `2026-09-16T00:47:51+0300 zeonux p2pool[123]: 2026-09-16 00:47:51.5612 P2Pool Your wallet 4AAAA got a payout of 0.001234567890 XMR in block 3763276
2026-09-16T01:00:00+0300 zeonux p2pool[123]: 2026-09-16 01:00:00.0000 P2Pool Your wallet 4AAAA didn't get a payout in block 3763300 because you had no shares in PPLNS window
2026-09-17T10:00:00+0300 zeonux p2pool[123]: 2026-09-17 10:00:00.0000 P2Pool Your wallet 4AAAA got a payout of 1.000000000000 XMR in block 3763999
garbage line
`

func TestParse(t *testing.T) {
	r := Parse("p.service", strings.NewReader(journal))
	if len(r.Payouts) != 2 || r.BlocksWithout != 1 {
		t.Fatalf("%+v", r)
	}
	if r.Payouts[0].AtomicUnits != 1234567890 || r.Payouts[0].Block != 3763276 || r.Payouts[0].At.Format("2006-01-02T15:04") != "2026-09-15T21:47" {
		t.Fatalf("first %+v", r.Payouts[0])
	}
	if r.TotalAtomicUnits != 1_000_000_000_000+1234567890 || r.TotalXMR != "1.001234567890" {
		t.Fatalf("total %d %s", r.TotalAtomicUnits, r.TotalXMR)
	}
	if FormatXMR(5) != "0.000000000005" {
		t.Fatal(FormatXMR(5))
	}
	if e := Parse("p.service", strings.NewReader("")); len(e.Payouts) != 0 || e.TotalXMR != "0.000000000000" {
		t.Fatalf("empty %+v", e)
	}
}

// PAY-03: a user outside systemd-journal gets an access error, not "no payouts".
func TestFetchAccess(t *testing.T) {
	if args := journalArgs("p.service", ""); slices.Contains(args, "-q") {
		t.Fatalf("%v: -q would hide the access hint", args)
	}
	hint := "Hint: You are currently not seeing messages from other users and the system.\n"
	for _, c := range []struct {
		stderr string
		err    error
		want   error
	}{
		{hint, errors.New("exit status 1"), ErrNoAccess},
		{hint, nil, ErrNoAccess},
		{"", nil, nil},
		{"Failed to open journal", errors.New("exit status 1"), nil},
	} {
		r, err := finish(Parse("p.service", strings.NewReader("-- No entries --\n")), c.stderr, c.err)
		switch {
		case c.want != nil && !errors.Is(err, c.want),
			c.want == nil && c.err == nil && (err != nil || len(r.Payouts) != 0),
			c.want == nil && c.err != nil && err == nil:
			t.Fatalf("stderr %q: %v %+v", c.stderr, err, r)
		}
	}
}
