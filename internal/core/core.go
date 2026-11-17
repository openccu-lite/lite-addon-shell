// Package core holds the addon's switch and what follows from it: off after the install, on for
// a time an administrator chooses, a notice on the system's Status page while it is on, and every
// session ended when it goes off. It also keeps the open sessions honest: each belongs to a web
// session, and ends when that web session ends (the system is asked every 15 seconds, in a way
// that does not keep the web session alive), when SSH is switched off, and when the system runs
// without a login.
package core

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/openccu-lite/lite-addon-shell/internal/config"
	"github.com/openccu-lite/lite-addon-shell/internal/sessions"
	"github.com/openccu-lite/lite-addon-shell/internal/sshc"
	"github.com/openccu-lite/lite-addon-shell/internal/system"
)

// Asker asks the system about a web session (system.Client; a fake in the tests).
type Asker interface {
	Session(ctx context.Context, sid string, peek bool) (system.State, error)
}

// Notices posts and clears the Status page's notice (system.Notifier; a fake in the tests).
type Notices interface {
	Enabled(ctx context.Context, addonID string, until *time.Time, now time.Time) error
	Clear(ctx context.Context) error
}

// Options configure a Core.
type Options struct {
	AddonID  string
	Store    *config.Store
	Sessions *sessions.Manager
	// Probe asks sshd what it offers (sshc.Client.Probe).
	Probe   func(ctx context.Context) string
	System  Asker
	Notices Notices
	Audit   func(format string, args ...any)
	Log     func(format string, args ...any)
	Now     func() time.Time
	// OnChange is called when the switch or the SSH state changed (the page's event stream).
	OnChange func()
	// Every is how often the web sessions and sshd are asked; 15 s by default.
	Every time.Duration
	// Unreachable is how long the system may not answer before a session it could not be asked
	// about is ended; 2 min by default.
	Unreachable time.Duration
}

// ErrAuthOff refuses the switch while the system runs without a login.
var ErrAuthOff = errors.New("the system runs without a login: every caller would be an administrator")

// ErrWindow refuses a length the switch does not offer.
var ErrWindow = errors.New("not a length the switch offers")

// Core is the switch and the watch.
type Core struct {
	o  Options
	mu sync.Mutex
	// ssh is the last probe's answer and when it was taken
	ssh   string
	sshAt time.Time
	// since when each web session could not be asked about
	unasked map[string]time.Time
	// peek is whether the system honoured ?peek=1 at the last answer (nil: not asked yet)
	peek       *bool
	warnedPeek bool
}

// New makes a Core.
func New(o Options) *Core {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Audit == nil {
		o.Audit = func(string, ...any) {}
	}
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	if o.OnChange == nil {
		o.OnChange = func() {}
	}
	if o.Every == 0 {
		o.Every = 15 * time.Second
	}
	if o.Unreachable == 0 {
		o.Unreachable = 2 * time.Minute
	}
	return &Core{o: o, unasked: map[string]time.Time{}}
}

// Switch answers the switch and whether it is on now.
func (c *Core) Switch() (config.Switch, bool) {
	sw := c.o.Store.Switch()
	return sw, sw.Active(c.o.Now())
}

// Enable switches the addon on for hours (0: until switched off), in the name of an account.
func (c *Core) Enable(ctx context.Context, by string, hours int, authOff bool) error {
	if authOff {
		return ErrAuthOff
	}
	if !config.ValidWindow(hours) {
		return ErrWindow
	}
	now := c.o.Now()
	sw := config.Switch{On: true, By: by, At: &now}
	if hours > 0 {
		u := now.Add(time.Duration(hours) * time.Hour)
		sw.Until = &u
	}
	if err := c.o.Store.SetSwitch(sw); err != nil {
		return err
	}
	until := "off"
	if sw.Until != nil {
		until = sw.Until.UTC().Format(time.RFC3339)
	}
	c.o.Audit("enabled until=%s by=%s", until, field(by))
	c.notice(ctx, sw)
	c.o.OnChange()
	return nil
}

// Disable switches the addon off and ends every session; why is for the audit line ("user",
// "expired", "auth-off").
func (c *Core) Disable(ctx context.Context, by, why string) error {
	sw, _ := c.Switch()
	if !sw.On {
		return nil
	}
	if err := c.o.Store.SetSwitch(config.Switch{}); err != nil {
		return err
	}
	n := c.o.Sessions.EndAll(sessions.ReasonAddonOff)
	c.o.Audit("disabled by=%s reason=%s sessions_ended=%d", field(by), why, n)
	c.notice(ctx, config.Switch{})
	c.o.OnChange()
	return nil
}

// notice brings the Status page's notice in line with the switch; a failure is logged, never
// a reason to refuse the switch (the addon's own page shows the state in any case).
func (c *Core) notice(ctx context.Context, sw config.Switch) {
	if c.o.Notices == nil {
		return
	}
	var err error
	if sw.Active(c.o.Now()) {
		err = c.o.Notices.Enabled(ctx, c.o.AddonID, sw.Until, c.o.Now())
	} else {
		err = c.o.Notices.Clear(ctx)
	}
	if err != nil {
		c.o.Log("the notice on the Status page could not be updated: %v", err)
	}
}

// SSH answers what sshd offers, asked at most every 5 seconds.
func (c *Core) SSH(ctx context.Context) string {
	c.mu.Lock()
	if c.ssh != "" && c.o.Now().Sub(c.sshAt) < 5*time.Second {
		defer c.mu.Unlock()
		return c.ssh
	}
	c.mu.Unlock()
	return c.probe(ctx)
}

func (c *Core) probe(ctx context.Context) string {
	st := sshc.StateUnknown
	if c.o.Probe != nil {
		st = c.o.Probe(ctx)
	}
	c.mu.Lock()
	changed := c.ssh != "" && c.ssh != st
	c.ssh, c.sshAt = st, c.o.Now()
	c.mu.Unlock()
	if changed {
		c.o.OnChange()
	}
	return st
}

// Peek answers whether the system's session check leaves the idle timer alone; nil before the
// first answer.
func (c *Core) Peek() *bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peek
}

// Tick is one round of the watch: the switch's time, then (when due says so) the web sessions and
// sshd. Run calls it every second.
func (c *Core) Tick(ctx context.Context, due bool) {
	sw := c.o.Store.Switch()
	if sw.On && !sw.Active(c.o.Now()) {
		if err := c.Disable(ctx, "-", "expired"); err != nil {
			c.o.Log("the switch could not be stored: %v", err)
		}
	}
	if !due {
		return
	}
	owners := c.o.Sessions.Owners()
	if len(owners) == 0 {
		c.mu.Lock()
		clear(c.unasked)
		c.mu.Unlock()
		return
	}
	// SSH switched off: sshd's listener is gone; the sessions end here even where sshd left its
	// children running
	if c.probe(ctx) == sshc.StateOff {
		c.o.Sessions.EndAll(sessions.ReasonSSHOff)
		return
	}
	now := c.o.Now()
	for _, sid := range owners {
		st, err := c.o.System.Session(ctx, sid, true)
		if err != nil {
			c.mu.Lock()
			first, ok := c.unasked[sid]
			if !ok {
				first = now
				c.unasked[sid] = now
			}
			c.mu.Unlock()
			if now.Sub(first) >= c.o.Unreachable {
				c.o.Log("the system could not be asked about a web session for %s: its sessions end", c.o.Unreachable)
				c.o.Sessions.EndWhere(sessions.ReasonUnverified, func(i sessions.Info) bool { return i.Owner == sid })
			}
			continue
		}
		c.mu.Lock()
		delete(c.unasked, sid)
		p := st.Peek
		c.peek = &p
		warn := !p && !c.warnedPeek
		if warn {
			c.warnedPeek = true
		}
		c.mu.Unlock()
		if warn {
			c.o.Log("WARNING: this system does not know the session check that leaves the idle timer alone (openccu-lite 2.0.0-beta.7 has it): an open terminal keeps its web session from idling out")
		}
		if st.AuthOff {
			// the login was switched off under the addon: everyone is an administrator now
			_ = c.Disable(ctx, "-", "auth-off")
			return
		}
		if !st.Admin(sid) {
			c.o.Sessions.EndWhere(sessions.ReasonSignedOut, func(i sessions.Info) bool { return i.Owner == sid })
		}
	}
}

// Run brings the notice in line with the stored switch, then watches until ctx ends.
func (c *Core) Run(ctx context.Context) {
	sw, on := c.Switch()
	if sw.On && !on {
		_ = c.Disable(ctx, "-", "expired")
	} else {
		c.notice(ctx, sw)
	}
	t := time.NewTicker(time.Second)
	defer t.Stop()
	last := c.o.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			due := c.o.Now().Sub(last) >= c.o.Every
			if due {
				last = c.o.Now()
			}
			c.Tick(ctx, due)
		}
	}
}

func field(s string) string {
	if s == "" {
		return "-"
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r <= ' ' || r == 0x7f || r == '=' || r == '"' {
			r = '_'
		}
		out = append(out, r)
		if len(out) == 64 {
			break
		}
	}
	return string(out)
}
