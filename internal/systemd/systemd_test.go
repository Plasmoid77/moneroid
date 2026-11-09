package systemd

import (
	"context"
	"testing"
)

const showOut = "Id=a.service\nLoadState=loaded\nActiveState=active\nAfter=b.service sysinit.target\n\nId=b.service\nLoadState=not-found\nActiveState=inactive\n"

func TestShow(t *testing.T) {
	var gotArgs []string
	run := func(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
		gotArgs = append([]string{name}, args...)
		return []byte(showOut), nil, nil
	}
	res, err := Show(context.Background(), run, "a.service", "b.service")
	if err != nil {
		t.Fatal(err)
	}
	if res["a.service"]["ActiveState"] != "active" || res["b.service"]["LoadState"] != "not-found" {
		t.Fatalf("unexpected %v", res)
	}
	if !res["a.service"].HasDependency("After", "b.service") || res["a.service"].HasDependency("After", "b") {
		t.Fatal("HasDependency")
	}
	if gotArgs[0] != "systemctl" || gotArgs[len(gotArgs)-3] != "--" {
		t.Fatalf("args %v", gotArgs)
	}
}

func TestControlAndJournalArgs(t *testing.T) {
	var got []string
	run := func(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
		got = append([]string{name}, args...)
		return nil, []byte("Access denied as the requested operation requires interactive authentication."), context.DeadlineExceeded
	}
	err := Control(context.Background(), run, "restart", "a.service", "b.service")
	if err == nil || !err.(*ControlError).Denied() {
		t.Fatalf("expected denied ControlError, got %v", err)
	}
	want := "systemctl --no-pager --no-ask-password restart -- a.service b.service"
	if s := join(got); s != want {
		t.Fatalf("args %q", s)
	}
	if s := join(JournalArgs([]string{"a.service"}, 50, true)); s != "--no-pager -o short-iso -n 50 -f -u a.service" {
		t.Fatalf("journal args %q", s)
	}
}

func join(a []string) string {
	s := ""
	for i, x := range a {
		if i > 0 {
			s += " "
		}
		s += x
	}
	return s
}

func TestResetStartLimit(t *testing.T) {
	var resets []string
	run := func(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
		if args[0] == "show" {
			return []byte("Id=a.service\nResult=start-limit-hit\n\nId=b.service\nResult=success\n"), nil, nil
		}
		resets = append(resets, args[len(args)-1])
		return nil, nil, nil
	}
	if err := ResetStartLimit(context.Background(), run, "a.service", "b.service"); err != nil || len(resets) != 1 || resets[0] != "a.service" {
		t.Fatalf("err %v resets %v", err, resets)
	}
}
