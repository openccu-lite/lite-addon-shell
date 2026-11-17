// Package sessions is the table of open root sessions: who holds which, the limits (how many at
// once, idle, longest), the lockout after wrong passwords, and the hand-over of a session's
// terminal bytes to the browser tab that holds its handle. A session is one SSH connection, owned
// by one web session of an administrator; it ends with that web session, with its limits, with
// the addon's switch and with SSH. Nothing typed and nothing shown is logged; the audit lines say
// who opened and closed what.
package sessions

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/openccu-lite/lite-addon-shell/internal/sshc"
)

// Why a session ended; the terminal shows the reason and the close line carries it.
const (
	ReasonExit       = "exit"       // the shell ended (exit, logout)
	ReasonIdle       = "idle"       // no input for the idle time
	ReasonMax        = "max"        // the longest session's length
	ReasonSignedOut  = "signed-out" // the web session that owned it ended
	ReasonEnded      = "ended"      // ended on the addon's page
	ReasonAddonOff   = "addon-off"  // the addon was switched off, or its time was up
	ReasonSSHOff     = "ssh-off"    // SSH was switched off
	ReasonTabClosed  = "tab-closed" // the tab went and did not come back within the grace
	ReasonConnection = "connection" // the SSH connection was lost
	ReasonStopping   = "stopping"   // the addon's service stops (an update, a reboot)
	ReasonUnverified = "unverified" // the system could not be asked about the web session
)

const (
	ringSize  = 256 << 10 // output kept for a tab that reattaches
	chunkSize = 32 << 10  // one read of terminal output

	// DefaultGrace is how long a session waits for its tab to come back (a reload).
	DefaultGrace = 30 * time.Second
	// LockoutFailures wrong passwords of one account or from one address lock the sign-in for
	// LockoutFor.
	LockoutFailures = 5
	LockoutFor      = 15 * time.Minute
)

// Shell is an open root shell (sshc.Shell; a fake in the tests).
type Shell interface {
	io.Reader
	io.Writer
	Resize(sshc.Size) error
	Done() <-chan struct{}
	Clean() bool
	Close()
}

// Opener signs in and opens a shell (sshc.Client behind a small adapter; a fake in the tests).
type Opener func(ctx context.Context, password string, size sshc.Size) (Shell, error)

// Limits are the limits in force (config.Settings as durations).
type Limits struct {
	Idle, Longest time.Duration
	MaxSessions   int
}

// Options configure a Manager.
type Options struct {
	Open   Opener
	Limits func() Limits
	// Audit writes one audit line to the journal.
	Audit func(format string, args ...any)
	// OnChange is called (outside the lock) when the table changed, for the page's event stream.
	OnChange func()
	Now      func() time.Time
	// Grace is how long a session without its tab lives; DefaultGrace by default.
	Grace time.Duration
}

// Who opens a session: the administrator's account, the web session that owns it, the client's
// address as the system's web server saw it.
type Who struct {
	User, SID, Client string
}

// Info is a session as the page lists it. Times are Unix milliseconds.
type Info struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	User     string `json:"user"`
	Client   string `json:"client"`
	Since    int64  `json:"since"`
	Input    int64  `json:"last_input"`
	IdleEnds int64  `json:"idle_ends"`
	Ends     int64  `json:"ends"`
	Attached bool   `json:"attached"`
	// Owner is the web session that owns it; never sent to a browser.
	Owner string `json:"-"`
}

// ErrNoHandle is answered to a tab that names no session or not its handle.
var ErrNoHandle = errors.New("no such session, or not its handle")

// TooManyError says the limit is reached, and who holds the sessions.
type TooManyError struct{ Sessions []Info }

func (e *TooManyError) Error() string { return "too many sessions are open" }

// LockedError says the sign-in is locked after wrong passwords.
type LockedError struct{ Until time.Time }

func (e *LockedError) Error() string { return "too many failed attempts" }

// WrongPasswordError carries the tries left before the lockout.
type WrongPasswordError struct{ TriesLeft int }

func (e *WrongPasswordError) Error() string { return "wrong password" }

type session struct {
	id, handle string
	who        Who
	since      time.Time
	input      time.Time
	shell      Shell

	ring []byte
	att  *Attachment
	// detached is when the session was last without a tab (its opening, a closed connection);
	// zero while a tab is attached
	detached time.Time
	ended    bool
	done     chan struct{} // closed at the end
}

// Attachment is a tab's hold on a session: the output so far, then the stream.
type Attachment struct {
	// Replay is the output the tab missed (the ring's content at the attach).
	Replay []byte
	// Out delivers terminal output; the sender waits for the receiver, so a slow tab slows the
	// shell instead of growing the daemon.
	Out chan []byte
	// Gone is closed when the attachment is over: the session ended (Reason says why) or another
	// tab took it over (Reason is "").
	Gone chan struct{}

	mu     sync.Mutex
	reason string
	once   sync.Once
}

// Reason is why the session ended, "" when the attachment was only replaced or detached.
func (a *Attachment) Reason() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reason
}

func (a *Attachment) close(reason string) {
	a.once.Do(func() {
		a.mu.Lock()
		a.reason = reason
		a.mu.Unlock()
		close(a.Gone)
	})
}

type failures struct {
	count int
	first time.Time
	until time.Time
}

// Manager is the table.
type Manager struct {
	o        Options
	mu       sync.Mutex
	sessions map[string]*session
	seq      int
	fails    map[string]*failures
}

// New makes a Manager.
func New(o Options) *Manager {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Grace == 0 {
		o.Grace = DefaultGrace
	}
	if o.Audit == nil {
		o.Audit = func(string, ...any) {}
	}
	if o.OnChange == nil {
		o.OnChange = func() {}
	}
	return &Manager{o: o, sessions: map[string]*session{}, fails: map[string]*failures{}}
}

func (m *Manager) info(s *session) Info {
	l := m.o.Limits()
	return Info{ID: s.id, Kind: "terminal", User: s.who.User, Client: s.who.Client, Owner: s.who.SID,
		Since: s.since.UnixMilli(), Input: s.input.UnixMilli(),
		IdleEnds: s.input.Add(l.Idle).UnixMilli(), Ends: s.since.Add(l.Longest).UnixMilli(), Attached: s.att != nil}
}

// List answers the open sessions, oldest first.
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listLocked()
}

func (m *Manager) listLocked() []Info {
	out := make([]Info, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, m.info(s))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Since != out[j].Since {
			return out[i].Since < out[j].Since
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Count is the number of open sessions.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

// Get answers one session.
func (m *Manager) Get(id string) (Info, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return Info{}, false
	}
	return m.info(s), true
}

func failKeys(who Who) []string { return []string{"user:" + who.User, "addr:" + who.Client} }

// lockedLocked answers until when the sign-in is locked for who, zero when it is not.
func (m *Manager) lockedLocked(who Who, now time.Time) time.Time {
	var until time.Time
	for _, k := range failKeys(who) {
		f := m.fails[k]
		if f == nil {
			continue
		}
		if !f.until.IsZero() && now.Before(f.until) && f.until.After(until) {
			until = f.until
		}
		// a count that never reached the lockout is forgotten after the same time, a lockout
		// when it is over
		if (f.until.IsZero() && now.Sub(f.first) > LockoutFor) || (!f.until.IsZero() && !now.Before(f.until)) {
			delete(m.fails, k)
		}
	}
	return until
}

// failedLocked counts a wrong password and answers the tries left (0: locked now).
func (m *Manager) failedLocked(who Who, now time.Time) int {
	left := LockoutFailures
	for _, k := range failKeys(who) {
		f := m.fails[k]
		if f == nil {
			f = &failures{first: now}
			m.fails[k] = f
		}
		f.count++
		if f.count >= LockoutFailures {
			f.until = now.Add(LockoutFor)
		}
		if l := LockoutFailures - f.count; l < left {
			left = l
		}
	}
	if left < 0 {
		left = 0
	}
	return left
}

// Locked answers until when the sign-in is locked for who; zero when it is not.
func (m *Manager) Locked(who Who) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lockedLocked(who, m.o.Now())
}

// Open signs who in to sshd with the password and opens a session. The errors: *LockedError,
// *TooManyError, *WrongPasswordError, sshc.ErrOff, sshc.ErrKeysOnly, sshc.ErrHostKey, or the
// connection's. The handle is the secret a tab attaches with; it is answered once, here.
func (m *Manager) Open(ctx context.Context, who Who, password string, size sshc.Size) (Info, string, error) {
	now := m.o.Now()
	m.mu.Lock()
	if until := m.lockedLocked(who, now); !until.IsZero() {
		m.mu.Unlock()
		return Info{}, "", &LockedError{Until: until}
	}
	if len(m.sessions) >= m.o.Limits().MaxSessions {
		list := m.listLocked()
		m.mu.Unlock()
		return Info{}, "", &TooManyError{Sessions: list}
	}
	m.mu.Unlock()

	shell, err := m.o.Open(ctx, password, size)
	now = m.o.Now()
	if err != nil {
		if errors.Is(err, sshc.ErrWrongPassword) {
			m.mu.Lock()
			left := m.failedLocked(who, now)
			m.mu.Unlock()
			m.o.Audit("login-failed user=%s client=%s tries_left=%d", clean(who.User), clean(who.Client), left)
			if left == 0 {
				m.o.Audit("login-locked user=%s client=%s until=%s", clean(who.User), clean(who.Client), now.Add(LockoutFor).UTC().Format(time.RFC3339))
			}
			return Info{}, "", &WrongPasswordError{TriesLeft: left}
		}
		return Info{}, "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		shell.Close()
		return Info{}, "", err
	}
	m.mu.Lock()
	// the limit again: two sign-ins may have passed the first check together
	if len(m.sessions) >= m.o.Limits().MaxSessions {
		list := m.listLocked()
		m.mu.Unlock()
		shell.Close()
		return Info{}, "", &TooManyError{Sessions: list}
	}
	for _, k := range failKeys(who) {
		delete(m.fails, k)
	}
	m.seq++
	s := &session{id: strconv.Itoa(m.seq), handle: hex.EncodeToString(raw), who: who, since: now, input: now, shell: shell,
		detached: now, done: make(chan struct{})}
	m.sessions[s.id] = s
	info := m.info(s)
	m.mu.Unlock()
	m.o.Audit("session-open kind=terminal user=%s client=%s id=%s auth=password", clean(who.User), clean(who.Client), s.id)
	go m.pump(s)
	go func() {
		select {
		case <-shell.Done():
			reason := ReasonExit
			if !shell.Clean() {
				reason = ReasonConnection
			}
			m.End(s.id, reason)
		case <-s.done:
		}
	}()
	m.o.OnChange()
	return info, s.handle, nil
}

// pump moves the shell's output into the ring and to the attached tab.
func (m *Manager) pump(s *session) {
	buf := make([]byte, chunkSize)
	for {
		n, err := s.shell.Read(buf)
		if n > 0 {
			data := append([]byte(nil), buf[:n]...)
			m.mu.Lock()
			if s.ended {
				m.mu.Unlock()
				return
			}
			s.ring = append(s.ring, data...)
			if over := len(s.ring) - ringSize; over > 0 {
				s.ring = append([]byte(nil), s.ring[over:]...)
			}
			att := s.att
			m.mu.Unlock()
			if att != nil {
				select {
				case att.Out <- data:
				case <-att.Gone:
				case <-s.done:
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// Attach hands the session to the tab that knows its handle. A tab already attached is replaced
// (its attachment ends with no reason): a reload reattaches before the old connection is gone.
func (m *Manager) Attach(id, handle string) (*Attachment, Info, error) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok || subtle.ConstantTimeCompare([]byte(s.handle), []byte(handle)) != 1 {
		m.mu.Unlock()
		return nil, Info{}, ErrNoHandle
	}
	old := s.att
	a := &Attachment{Replay: append([]byte(nil), s.ring...), Out: make(chan []byte), Gone: make(chan struct{})}
	s.att = a
	s.detached = time.Time{}
	info := m.info(s)
	m.mu.Unlock()
	if old != nil {
		old.close("")
	}
	m.o.OnChange()
	return a, info, nil
}

// Detach is the tab's connection closing: the session waits for the grace.
func (m *Manager) Detach(id string, a *Attachment) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	mine := ok && s.att == a
	if mine {
		s.att = nil
		s.detached = m.o.Now()
	}
	m.mu.Unlock()
	a.close("")
	if mine {
		m.o.OnChange()
	}
}

// Input is what the user typed or pasted; it resets the idle time.
func (m *Manager) Input(id string, a *Attachment, data []byte) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok || s.att != a {
		m.mu.Unlock()
		return ErrNoHandle
	}
	s.input = m.o.Now()
	shell := s.shell
	m.mu.Unlock()
	_, err := shell.Write(data)
	return err
}

// Resize tells the session's pty its size; it is not input.
func (m *Manager) Resize(id string, a *Attachment, size sshc.Size) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok || s.att != a {
		m.mu.Unlock()
		return ErrNoHandle
	}
	shell := s.shell
	m.mu.Unlock()
	return shell.Resize(size)
}

// End ends one session; false when there is none of that id.
func (m *Manager) End(id, reason string) bool {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return false
	}
	m.endLocked(s, reason)
	m.mu.Unlock()
	m.o.OnChange()
	return true
}

func (m *Manager) endLocked(s *session, reason string) {
	if s.ended {
		return
	}
	s.ended = true
	delete(m.sessions, s.id)
	close(s.done)
	if s.att != nil {
		s.att.close(reason)
		s.att = nil
	}
	s.ring = nil
	go s.shell.Close()
	m.o.Audit("session-close id=%s user=%s reason=%s duration=%d", s.id, clean(s.who.User), reason, int(m.o.Now().Sub(s.since).Seconds()))
}

// EndWhere ends every session the test admits (all of them for nil) and answers how many.
func (m *Manager) EndWhere(reason string, test func(Info) bool) int {
	m.mu.Lock()
	n := 0
	for _, s := range m.sessions {
		if test == nil || test(m.info(s)) {
			m.endLocked(s, reason)
			n++
		}
	}
	m.mu.Unlock()
	if n > 0 {
		m.o.OnChange()
	}
	return n
}

// EndAll ends every session.
func (m *Manager) EndAll(reason string) int { return m.EndWhere(reason, nil) }

// Owners lists the web sessions that own a session here.
func (m *Manager) Owners() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, s := range m.sessions {
		if !seen[s.who.SID] {
			seen[s.who.SID] = true
			out = append(out, s.who.SID)
		}
	}
	sort.Strings(out)
	return out
}

// Sweep ends the sessions whose time is up: idle, the longest length, a tab gone for the grace.
// Run calls it every second; a test calls it with its own clock.
func (m *Manager) Sweep() {
	now := m.o.Now()
	l := m.o.Limits()
	m.mu.Lock()
	n := 0
	for _, s := range m.sessions {
		switch {
		case now.Sub(s.since) >= l.Longest:
			m.endLocked(s, ReasonMax)
		case now.Sub(s.input) >= l.Idle:
			m.endLocked(s, ReasonIdle)
		case s.att == nil && !s.detached.IsZero() && now.Sub(s.detached) >= m.o.Grace:
			m.endLocked(s, ReasonTabClosed)
		default:
			continue
		}
		n++
	}
	m.mu.Unlock()
	if n > 0 {
		m.o.OnChange()
	}
}

// Run sweeps every second until ctx ends, then ends every session.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.EndAll(ReasonStopping)
			return
		case <-t.C:
			m.Sweep()
		}
	}
}

// clean keeps an audit field one token: no spaces, no control characters, a bounded length.
func clean(s string) string {
	if s == "" {
		return "-"
	}
	b := make([]rune, 0, len(s))
	for _, r := range s {
		if r <= ' ' || r == 0x7f || r == '=' || r == '"' {
			r = '_'
		}
		b = append(b, r)
		if len(b) == 64 {
			break
		}
	}
	return string(b)
}
