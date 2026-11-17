package core

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openccu-lite/lite-addon-shell/internal/config"
	"github.com/openccu-lite/lite-addon-shell/internal/sessions"
	"github.com/openccu-lite/lite-addon-shell/internal/sshc"
	"github.com/openccu-lite/lite-addon-shell/internal/system"
)

type shell struct {
	r    *io.PipeReader
	w    *io.PipeWriter
	done chan struct{}
	once sync.Once
}

func newShell() *shell {
	r, w := io.Pipe()
	return &shell{r: r, w: w, done: make(chan struct{})}
}
func (s *shell) Read(p []byte) (int, error)  { return s.r.Read(p) }
func (s *shell) Write(p []byte) (int, error) { return len(p), nil }
func (s *shell) Resize(sshc.Size) error      { return nil }
func (s *shell) Done() <-chan struct{}       { return s.done }
func (s *shell) Clean() bool                 { return true }
func (s *shell) Close()                      { s.once.Do(func() { s.w.Close(); close(s.done) }) }

// asker is the system as the watch sees it.
type asker struct {
	mu      sync.Mutex
	live    map[string]string // sid -> level
	authOff bool
	down    bool
	old     bool
	asks    []string // "sid peek" / "sid touch"
}

func (a *asker) Session(_ context.Context, sid string, peek bool) (system.State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asks = append(a.asks, sid+map[bool]string{true: " peek", false: " touch"}[peek])
	if a.down {
		return system.State{}, errors.New("HTTP 503")
	}
	st := system.State{Peek: peek && !a.old, AuthOff: a.authOff}
	if level, ok := a.live[sid]; ok {
		st.Authenticated, st.SID, st.Level, st.User = true, sid, level, "admin"
	}
	return st, nil
}

type notices struct {
	mu    sync.Mutex
	calls []string
	until *time.Time
	fail  bool
}

func (n *notices) Enabled(_ context.Context, id string, until *time.Time, _ time.Time) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, "enabled:"+id)
	n.until = until
	if n.fail {
		return errors.New("no token")
	}
	return nil
}
func (n *notices) Clear(context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, "clear")
	return nil
}
func (n *notices) list() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return strings.Join(n.calls, ",")
}

type env struct {
	c     *Core
	m     *sessions.Manager
	store *config.Store
	a     *asker
	n     *notices
	mu    sync.Mutex
	now   time.Time
	ssh   string
	audit []string
}

const sidA, sidB = "AAAAAAAAAAAAAAAAAAAAAAAAAA", "BBBBBBBBBBBBBBBBBBBBBBBBBB"

func newEnv(t *testing.T) *env {
	e := &env{now: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC), ssh: sshc.StateOn,
		a: &asker{live: map[string]string{sidA: "administer", sidB: "administer"}}, n: &notices{}}
	e.store, _ = config.Open(t.TempDir())
	now := func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now }
	audit := func(format string, args ...any) {
		e.mu.Lock()
		e.audit = append(e.audit, format)
		e.mu.Unlock()
	}
	e.m = sessions.New(sessions.Options{
		Open: func(context.Context, string, sshc.Size) (sessions.Shell, error) { return newShell(), nil },
		Limits: func() sessions.Limits {
			return sessions.Limits{Idle: time.Hour, Longest: 8 * time.Hour, MaxSessions: 4}
		},
		Now: now, Audit: audit, Grace: 24 * time.Hour,
	})
	e.c = New(Options{AddonID: "shell", Store: e.store, Sessions: e.m, System: e.a, Notices: e.n, Now: now, Audit: audit,
		Probe: func(context.Context) string { e.mu.Lock(); defer e.mu.Unlock(); return e.ssh }})
	t.Cleanup(func() { e.m.EndAll(sessions.ReasonStopping) })
	return e
}

func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}

func (e *env) open(t *testing.T, sid string) {
	t.Helper()
	if _, _, err := e.m.Open(context.Background(), sessions.Who{User: "admin", SID: sid, Client: "192.0.2.1"}, "x", sshc.Size{}); err != nil {
		t.Fatal(err)
	}
}

func (e *env) audited(prefix string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, l := range e.audit {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

// Off after the install; switched on for eight hours it posts the Status page's notice with that
// end, and goes off by itself when the time is up - ending every session and clearing the notice.
func TestSwitchWindow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, on := e.c.Switch(); on {
		t.Fatal("on after the install")
	}
	if err := e.c.Enable(ctx, "admin", 3, false); !errors.Is(err, ErrWindow) {
		t.Fatalf("three hours: %v", err)
	}
	if err := e.c.Enable(ctx, "admin", 8, false); err != nil {
		t.Fatal(err)
	}
	sw, on := e.c.Switch()
	if !on || sw.By != "admin" || !sw.Until.Equal(e.now.Add(8*time.Hour)) {
		t.Fatalf("switch %+v", sw)
	}
	if e.n.list() != "enabled:shell" || !e.n.until.Equal(*sw.Until) {
		t.Fatalf("notices %s", e.n.list())
	}
	e.open(t, sidA)
	e.advance(8*time.Hour - time.Second)
	e.c.Tick(ctx, false)
	if _, on := e.c.Switch(); !on || e.m.Count() != 1 {
		t.Fatal("off before its time")
	}
	e.advance(time.Second)
	e.c.Tick(ctx, false)
	if sw, on := e.c.Switch(); on || sw.On {
		t.Fatal("still on after eight hours")
	}
	if e.m.Count() != 0 {
		t.Fatal("a session outlives the switch")
	}
	if e.n.list() != "enabled:shell,clear" {
		t.Fatalf("notices %s", e.n.list())
	}
	if e.audited("enabled ") != 1 || e.audited("disabled ") != 1 {
		t.Errorf("audit %v", e.audit)
	}
	// until switched off: no end, and the user's off ends the sessions
	if err := e.c.Enable(ctx, "admin", 0, false); err != nil {
		t.Fatal(err)
	}
	e.advance(1000 * time.Hour)
	e.c.Tick(ctx, false)
	if sw, on := e.c.Switch(); !on || sw.Until != nil {
		t.Fatal("a switch without an end ended")
	}
	e.open(t, sidA)
	if err := e.c.Disable(ctx, "admin", "user"); err != nil {
		t.Fatal(err)
	}
	if e.m.Count() != 0 {
		t.Fatal("a session outlives the switch-off")
	}
}

// The switch works when the notice cannot be posted, and is refused while the system has no login.
func TestSwitchGuards(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.n.fail = true
	if err := e.c.Enable(ctx, "admin", 1, false); err != nil {
		t.Fatalf("a failing notice refused the switch: %v", err)
	}
	_ = e.c.Disable(ctx, "admin", "user")
	if err := e.c.Enable(ctx, "admin", 8, true); !errors.Is(err, ErrAuthOff) {
		t.Fatalf("without a login: %v", err)
	}
	if _, on := e.c.Switch(); on {
		t.Fatal("switched on without a login")
	}
}

// A session ends when the web session that owns it ends or is no administrator's any more - asked
// with the check that does not touch the session, never the other.
func TestFollowsWebSession(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_ = e.c.Enable(ctx, "admin", 8, false)
	e.open(t, sidA)
	e.open(t, sidB)
	e.c.Tick(ctx, true)
	if e.m.Count() != 2 {
		t.Fatal("live web sessions lost their shells")
	}
	if p := e.c.Peek(); p == nil || !*p {
		t.Fatal("the system honours the peek, and the core does not know")
	}
	e.a.mu.Lock()
	delete(e.a.live, sidA) // a logout
	e.a.mu.Unlock()
	e.c.Tick(ctx, true)
	if e.m.Count() != 1 || e.m.List()[0].Owner != sidB {
		t.Fatalf("after A's logout: %+v", e.m.List())
	}
	e.a.mu.Lock()
	e.a.live[sidB] = "configure" // the account was lowered
	e.a.mu.Unlock()
	e.c.Tick(ctx, true)
	if e.m.Count() != 0 {
		t.Fatal("a lowered account keeps its root shell")
	}
	e.a.mu.Lock()
	defer e.a.mu.Unlock()
	for _, ask := range e.a.asks {
		if !strings.HasSuffix(ask, " peek") {
			t.Fatalf("the watch touched a web session: %v", e.a.asks)
		}
	}
	if len(e.a.asks) != 5 {
		t.Errorf("asks %v", e.a.asks)
	}
}

// The system not answering (occulited restarting) ends nothing for two minutes, then the
// sessions it could not be asked about.
func TestSystemUnreachable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_ = e.c.Enable(ctx, "admin", 8, false)
	e.open(t, sidA)
	e.a.down = true
	e.c.Tick(ctx, true)
	e.advance(90 * time.Second)
	e.c.Tick(ctx, true)
	if e.m.Count() != 1 {
		t.Fatal("ended within two minutes of silence")
	}
	// it answers again in time: the count starts anew
	e.a.down = false
	e.c.Tick(ctx, true)
	e.a.down = true
	e.advance(time.Minute)
	e.c.Tick(ctx, true)
	e.advance(90 * time.Second)
	e.c.Tick(ctx, true)
	if e.m.Count() != 1 {
		t.Fatal("the earlier silence counted")
	}
	e.advance(31 * time.Second)
	e.c.Tick(ctx, true)
	if e.m.Count() != 0 {
		t.Fatal("a session nobody can vouch for lives on")
	}
}

// SSH switched off ends every session; the login switched off switches the addon off.
func TestSSHOffAndAuthOff(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_ = e.c.Enable(ctx, "admin", 8, false)
	e.open(t, sidA)
	e.mu.Lock()
	e.ssh = sshc.StateOff
	e.mu.Unlock()
	e.c.Tick(ctx, true)
	if e.m.Count() != 0 {
		t.Fatal("a session outlives SSH")
	}
	if _, on := e.c.Switch(); !on {
		t.Fatal("SSH off switched the addon off")
	}
	e.mu.Lock()
	e.ssh = sshc.StateOn
	e.mu.Unlock()
	e.open(t, sidA)
	e.a.authOff = true
	e.c.Tick(ctx, true)
	if _, on := e.c.Switch(); on || e.m.Count() != 0 {
		t.Fatal("the addon stays on without a login")
	}
}

// On a system from before the peek the watch still works, and says so once.
func TestOldSystem(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var logs []string
	e.c.o.Log = func(format string, _ ...any) { logs = append(logs, format) }
	e.a.old = true
	_ = e.c.Enable(ctx, "admin", 8, false)
	e.open(t, sidA)
	e.c.Tick(ctx, true)
	e.c.Tick(ctx, true)
	if p := e.c.Peek(); p == nil || *p {
		t.Fatal("peek not reported as missing")
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "2.0.0-beta.7") {
		t.Fatalf("logs %v", logs)
	}
	if e.m.Count() != 1 {
		t.Fatal("ended on an old system")
	}
}

// At its start the daemon brings the notice in line with the stored switch: cleared when off,
// posted again when on, and a switch whose time ran out while it was down goes off.
func TestStart(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.c.Run(ctx); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for e.n.list() != "clear" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if e.n.list() != "clear" {
		t.Fatalf("off at the start: %s", e.n.list())
	}
	now := e.now
	past := now.Add(-time.Minute)
	_ = e.store.SetSwitch(config.Switch{On: true, Until: &past, By: "admin", At: &now})
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() { e.c.Run(ctx); close(done) }()
	for e.store.Switch().On && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if e.store.Switch().On {
		t.Fatal("a switch whose time ran out stays on")
	}
}
