package sessions

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openccu-lite/lite-addon-shell/internal/sshc"
)

// fakeShell is a shell whose output the test writes and whose input it reads back.
type fakeShell struct {
	out    *io.PipeReader
	outW   *io.PipeWriter
	mu     sync.Mutex
	in     []byte
	size   sshc.Size
	done   chan struct{}
	once   sync.Once
	clean  bool
	closed bool
}

func newFakeShell() *fakeShell {
	r, w := io.Pipe()
	return &fakeShell{out: r, outW: w, done: make(chan struct{}), clean: true}
}
func (f *fakeShell) Read(p []byte) (int, error) { return f.out.Read(p) }
func (f *fakeShell) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.in = append(f.in, p...)
	return len(p), nil
}
func (f *fakeShell) Resize(s sshc.Size) error {
	f.mu.Lock()
	f.size = s
	f.mu.Unlock()
	return nil
}
func (f *fakeShell) Done() <-chan struct{} { return f.done }
func (f *fakeShell) Clean() bool           { return f.clean }
func (f *fakeShell) Close() {
	f.once.Do(func() {
		f.mu.Lock()
		f.closed = true
		f.mu.Unlock()
		f.outW.Close()
		close(f.done)
	})
}
func (f *fakeShell) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}
func (f *fakeShell) input() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.in)
}

type env struct {
	t      *testing.T
	m      *Manager
	now    time.Time
	mu     sync.Mutex
	shells []*fakeShell
	audit  []string
	limits Limits
}

func newEnv(t *testing.T) *env {
	e := &env{t: t, now: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC), limits: Limits{Idle: 15 * time.Minute, Longest: 8 * time.Hour, MaxSessions: 2}}
	e.m = New(Options{
		Open: func(_ context.Context, password string, _ sshc.Size) (Shell, error) {
			if password != "root-pass" {
				return nil, sshc.ErrWrongPassword
			}
			sh := newFakeShell()
			e.mu.Lock()
			e.shells = append(e.shells, sh)
			e.mu.Unlock()
			return sh, nil
		},
		Limits: func() Limits { e.mu.Lock(); defer e.mu.Unlock(); return e.limits },
		Audit: func(format string, args ...any) {
			e.mu.Lock()
			e.audit = append(e.audit, strings.SplitN(format, " ", 2)[0])
			e.mu.Unlock()
		},
		Now: func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now },
	})
	t.Cleanup(func() { e.m.EndAll(ReasonStopping) })
	return e
}

func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
	e.m.Sweep()
}

func (e *env) open(who Who) (Info, string) {
	e.t.Helper()
	info, handle, err := e.m.Open(context.Background(), who, "root-pass", sshc.Size{})
	if err != nil {
		e.t.Fatal(err)
	}
	return info, handle
}

func (e *env) audited() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return strings.Join(e.audit, " ")
}

var alice = Who{User: "alice", SID: "SIDA", Client: "192.0.2.10"}
var bob = Who{User: "bob", SID: "SIDB", Client: "192.0.2.11"}

func waitGone(t *testing.T, a *Attachment) string {
	t.Helper()
	select {
	case <-a.Gone:
	case <-time.After(3 * time.Second):
		t.Fatal("the attachment did not end")
	}
	return a.Reason()
}

// At most two sessions at once, over all accounts; the refusal says who holds them, and a place
// is free again when one ends.
func TestLimit(t *testing.T) {
	e := newEnv(t)
	a, _ := e.open(alice)
	e.open(bob)
	_, _, err := e.m.Open(context.Background(), alice, "root-pass", sshc.Size{})
	var many *TooManyError
	if !errors.As(err, &many) || len(many.Sessions) != 2 || many.Sessions[0].User != "alice" || many.Sessions[1].User != "bob" {
		t.Fatalf("a third session: %v %+v", err, many)
	}
	if len(e.shells) != 2 {
		t.Fatal("the refused sign-in reached sshd")
	}
	e.m.End(a.ID, ReasonEnded)
	e.open(alice)
	if e.m.Count() != 2 {
		t.Fatalf("count %d", e.m.Count())
	}
}

// Five wrong passwords of one account or from one address lock the sign-in for 15 minutes, the
// right password included; a right one before that clears the count.
func TestLockout(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := 1; i <= 4; i++ {
		_, _, err := e.m.Open(ctx, alice, "nope", sshc.Size{})
		var w *WrongPasswordError
		if !errors.As(err, &w) || w.TriesLeft != LockoutFailures-i {
			t.Fatalf("try %d: %v", i, err)
		}
	}
	// the right one clears it
	e.open(alice)
	e.m.EndAll(ReasonEnded)
	for i := 1; i <= 5; i++ {
		_, _, _ = e.m.Open(ctx, alice, "nope", sshc.Size{})
	}
	var locked *LockedError
	if _, _, err := e.m.Open(ctx, alice, "root-pass", sshc.Size{}); !errors.As(err, &locked) || !locked.Until.Equal(e.now.Add(LockoutFor)) {
		t.Fatalf("after five: %v", err)
	}
	// the same account from another address, and another account from the same address
	if _, _, err := e.m.Open(ctx, Who{User: "alice", SID: "X", Client: "192.0.2.99"}, "root-pass", sshc.Size{}); !errors.As(err, &locked) {
		t.Fatalf("the account from elsewhere: %v", err)
	}
	if _, _, err := e.m.Open(ctx, Who{User: "carol", SID: "Y", Client: alice.Client}, "root-pass", sshc.Size{}); !errors.As(err, &locked) {
		t.Fatalf("another account from the address: %v", err)
	}
	e.open(bob) // somebody else, from elsewhere, is not locked
	e.advance(LockoutFor + time.Second)
	e.open(alice)
	if got := e.audited(); !strings.Contains(got, "login-failed") || !strings.Contains(got, "login-locked") {
		t.Errorf("audit: %s", got)
	}
}

// A session ends after the idle time without input - output does not count, nor does a resize -
// and at the longest session's length whatever the input.
func TestIdleAndLongest(t *testing.T) {
	e := newEnv(t)
	info, handle := e.open(alice)
	att, _, err := e.m.Attach(info.ID, handle)
	if err != nil {
		t.Fatal(err)
	}
	e.advance(10 * time.Minute)
	if err := e.m.Input(info.ID, att, []byte("ls\r")); err != nil {
		t.Fatal(err)
	}
	if e.shells[0].input() != "ls\r" {
		t.Fatalf("input %q", e.shells[0].input())
	}
	e.advance(10 * time.Minute) // 20 min after the start, 10 after the input
	if e.m.Count() != 1 {
		t.Fatal("ended although there was input")
	}
	// output and a resize are not input
	go func() { _, _ = e.shells[0].outW.Write([]byte("tail -f output")) }()
	select {
	case d := <-att.Out:
		if string(d) != "tail -f output" {
			t.Fatalf("output %q", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no output")
	}
	_ = e.m.Resize(info.ID, att, sshc.Size{Cols: 90, Rows: 20})
	e.advance(5*time.Minute + time.Second)
	if r := waitGone(t, att); r != ReasonIdle {
		t.Fatalf("reason %q", r)
	}
	if e.m.Count() != 0 {
		t.Fatal("still listed")
	}

	// the longest length: input every ten minutes for eight hours
	info, handle = e.open(alice)
	att, _, _ = e.m.Attach(info.ID, handle)
	for i := 0; i < 47; i++ {
		e.advance(10 * time.Minute)
		if err := e.m.Input(info.ID, att, []byte("x")); err != nil {
			t.Fatalf("after %d min: %v", (i+1)*10, err)
		}
	}
	e.advance(10 * time.Minute)
	if r := waitGone(t, att); r != ReasonMax {
		t.Fatalf("reason %q", r)
	}
	time.Sleep(50 * time.Millisecond)
	if !e.shells[1].isClosed() {
		t.Error("the SSH connection stays open")
	}
}

// The handle is the tab's secret: without it nothing attaches. A second tab with it takes the
// session over; a closed tab leaves the session for the grace, in which the output is kept.
func TestAttach(t *testing.T) {
	e := newEnv(t)
	info, handle := e.open(alice)
	if _, _, err := e.m.Attach(info.ID, "0000"); !errors.Is(err, ErrNoHandle) {
		t.Fatalf("a wrong handle: %v", err)
	}
	if _, _, err := e.m.Attach("99", handle); !errors.Is(err, ErrNoHandle) {
		t.Fatalf("another id: %v", err)
	}
	if len(handle) != 64 {
		t.Fatalf("the handle is %d characters", len(handle))
	}
	// output before any tab attached is replayed
	_, _ = e.shells[0].outW.Write([]byte("motd\r\n# "))
	var a1 *Attachment
	deadline := time.Now().Add(3 * time.Second)
	for {
		a, _, err := e.m.Attach(info.ID, handle)
		if err != nil {
			t.Fatal(err)
		}
		if string(a.Replay) == "motd\r\n# " {
			a1 = a
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("replay %q", a.Replay)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// a second tab takes over: the first is gone without a reason, and its input is refused
	a2, _, _ := e.m.Attach(info.ID, handle)
	if r := waitGone(t, a1); r != "" {
		t.Fatalf("replaced with reason %q", r)
	}
	if err := e.m.Input(info.ID, a1, []byte("x")); !errors.Is(err, ErrNoHandle) {
		t.Fatalf("input of the replaced tab: %v", err)
	}
	// the tab closes: the session lives for the grace
	e.m.Detach(info.ID, a2)
	e.advance(DefaultGrace - time.Second)
	if e.m.Count() != 1 {
		t.Fatal("ended within the grace")
	}
	a3, _, err := e.m.Attach(info.ID, handle)
	if err != nil {
		t.Fatal(err)
	}
	e.advance(5 * time.Minute) // attached again: the grace does not run
	if e.m.Count() != 1 {
		t.Fatal("ended while attached")
	}
	e.m.Detach(info.ID, a3)
	e.advance(DefaultGrace)
	if e.m.Count() != 0 {
		t.Fatal("lives on without a tab")
	}
	// a session nobody ever attached to goes the same way
	e.open(bob)
	e.advance(DefaultGrace)
	if e.m.Count() != 0 {
		t.Fatal("a session without any tab lives on")
	}
}

// A session ends with its shell (the user's exit), and a lost connection is told apart; ending by
// owner ends exactly that web session's.
func TestEnds(t *testing.T) {
	e := newEnv(t)
	i1, h1 := e.open(alice)
	a1, _, _ := e.m.Attach(i1.ID, h1)
	e.shells[0].Close()
	if r := waitGone(t, a1); r != ReasonExit {
		t.Fatalf("reason %q", r)
	}
	i2, h2 := e.open(alice)
	a2, _, _ := e.m.Attach(i2.ID, h2)
	e.shells[1].clean = false
	e.shells[1].Close()
	if r := waitGone(t, a2); r != ReasonConnection {
		t.Fatalf("reason %q", r)
	}
	// a connection lost because SSH was switched off says so
	e.m.o.Lost = func() string { return ReasonSSHOff }
	ix, hx := e.open(alice)
	ax, _, _ := e.m.Attach(ix.ID, hx)
	e.shells[2].clean = false
	e.shells[2].Close()
	if r := waitGone(t, ax); r != ReasonSSHOff {
		t.Fatalf("reason %q", r)
	}
	i3, h3 := e.open(alice)
	i4, h4 := e.open(bob)
	a3, _, _ := e.m.Attach(i3.ID, h3)
	a4, _, _ := e.m.Attach(i4.ID, h4)
	if got := strings.Join(e.m.Owners(), ","); got != "SIDA,SIDB" {
		t.Fatalf("owners %s", got)
	}
	if n := e.m.EndWhere(ReasonSignedOut, func(i Info) bool { return i.Owner == "SIDA" }); n != 1 {
		t.Fatalf("ended %d", n)
	}
	if r := waitGone(t, a3); r != ReasonSignedOut {
		t.Fatalf("reason %q", r)
	}
	select {
	case <-a4.Gone:
		t.Fatal("bob's session ended with alice's web session")
	default:
	}
	if e.m.End("nope", ReasonEnded) {
		t.Error("ending a session that is not there")
	}
	if got := e.audited(); strings.Count(got, "session-open") != 5 || strings.Count(got, "session-close") != 4 {
		t.Errorf("audit: %s", got)
	}
}

// The ring keeps the newest output, bounded.
func TestRingBounded(t *testing.T) {
	e := newEnv(t)
	info, handle := e.open(alice)
	chunk := []byte(strings.Repeat("x", 64<<10))
	for i := 0; i < 6; i++ {
		_, _ = e.shells[0].outW.Write(chunk)
	}
	_, _ = e.shells[0].outW.Write([]byte("END"))
	deadline := time.Now().Add(3 * time.Second)
	for {
		a, _, _ := e.m.Attach(info.ID, handle)
		if strings.HasSuffix(string(a.Replay), "END") {
			if len(a.Replay) != ringSize {
				t.Fatalf("the ring holds %d bytes", len(a.Replay))
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the output did not arrive")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestClean(t *testing.T) {
	for in, want := range map[string]string{"": "-", "admin": "admin", "a b\nc=d\"e": "a_b_c_d_e", strings.Repeat("x", 100): strings.Repeat("x", 64)} {
		if got := clean(in); got != want {
			t.Errorf("clean(%q) = %q", in, got)
		}
	}
}
