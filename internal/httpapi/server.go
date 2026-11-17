// Package httpapi is the addon's own HTTP server: the page's files, the terminal page and the JSON
// API under one base path (/addons/shell/), which lighttpd proxies to the loopback port. Every
// request carries the system's web session, confirmed through the kit: administrators only, never
// an API token. Writes carry the kit's CSRF header. A root session is opened and used only over
// HTTPS (or through a tunnel addon when the administrator allowed that), and only by the web
// session that opened it. The readme documents the routes.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/ccu-addon-kit/go/addonkit"

	"github.com/openccu-lite/lite-addon-shell/internal/audit"
	"github.com/openccu-lite/lite-addon-shell/internal/config"
	"github.com/openccu-lite/lite-addon-shell/internal/core"
	"github.com/openccu-lite/lite-addon-shell/internal/sessions"
	"github.com/openccu-lite/lite-addon-shell/internal/sshc"
)

// Options configure the server.
type Options struct {
	// BasePath is where the page is served, /addons/shell (no trailing slash).
	BasePath string
	// WWW is the directory with the built page.
	WWW      string
	Store    *config.Store
	Core     *core.Core
	Sessions *sessions.Manager
	System   core.Asker
	Audit    *audit.Log
	// Wrap is the kit's session check (administrators, no tokens); nil: none, and every caller is
	// the account "dev" with the web session DevSID (development and the page's tests only).
	Wrap func(http.Handler) http.Handler
	// InsecureHTTP admits plain HTTP (development and the page's tests only).
	InsecureHTTP bool
	Version      string
	Started      time.Time
	Log          func(format string, args ...any)
}

// Routes lists every "METHOD /path" the API serves; the readme's table is held to it by a test.
var Routes []string

type server struct {
	o   Options
	hub *hub
}

// hub tells the pages' event streams that something changed.
type hub struct {
	mu   sync.Mutex
	subs map[chan struct{}]bool
}

func (h *hub) sub() chan struct{} {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.subs[ch] = true
	h.mu.Unlock()
	return ch
}

func (h *hub) unsub(ch chan struct{}) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

func (h *hub) ping() {
	h.mu.Lock()
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	h.mu.Unlock()
}

func handle(mux *http.ServeMux, pattern string, h http.HandlerFunc) {
	Routes = append(Routes, pattern)
	mux.HandleFunc(pattern, h)
}

// New builds the handler and answers the function that tells the pages something changed (the
// session table's and the switch's OnChange).
func New(o Options) (http.Handler, func()) {
	o.BasePath = strings.TrimRight(o.BasePath, "/")
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	s := &server{o: o, hub: &hub{subs: map[chan struct{}]bool{}}}
	Routes = nil
	api := http.NewServeMux()
	handle(api, "GET /api/v1/health", s.health)
	handle(api, "GET /api/v1/state", s.state)
	handle(api, "PUT /api/v1/enabled", s.putEnabled)
	handle(api, "PUT /api/v1/settings", s.putSettings)
	handle(api, "POST /api/v1/sessions", s.openSession)
	handle(api, "DELETE /api/v1/sessions", s.endAll)
	handle(api, "DELETE /api/v1/sessions/{id}", s.endSession)
	handle(api, "GET /api/v1/sessions/{id}/tty", s.tty)
	handle(api, "GET /api/v1/events", s.events)
	handle(api, "GET /api/v1/audit", s.audit)
	api.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		addonkit.WriteError(w, http.StatusNotFound, "not-found", r.URL.Path)
	})

	root := http.NewServeMux()
	root.Handle("/api/", addonkit.CSRF(api))
	root.Handle("/", pageHeaders(addonkit.AppFiles(o.WWW)))

	var h http.Handler = http.StripPrefix(o.BasePath, root)
	if o.Wrap != nil {
		h = o.Wrap(h)
	}
	return guard(h), s.hub.ping
}

// TerminalCSP is the terminal page's policy: its own scripts and connections only, and no frame
// around it - another page on the system's origin (an addon's) cannot embed the terminal and reach
// into it. xterm.js sets styles from script, hence style-src's 'unsafe-inline'.
const TerminalCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// pageHeaders sets the security headers of the page's files; the terminal page refuses framing.
func pageHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if p := strings.TrimSuffix(r.URL.Path, "/"); p == "/terminal.html" || p == "/terminal" {
			w.Header().Set("Content-Security-Policy", TerminalCSP)
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Cache-Control", "no-store")
			if p == "/terminal" {
				r = r.Clone(r.Context())
				r.URL.Path = "/terminal.html"
			}
		}
		next.ServeHTTP(w, r)
	})
}

func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := r.URL.Path; p != "/" && (path.Clean(p) != strings.TrimSuffix(p, "/") && path.Clean(p) != p) {
			http.NotFound(w, r)
			return
		}
		if strings.Contains(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fail answers an error with its code, a message and extra fields.
func fail(w http.ResponseWriter, status int, code, message string, extra map[string]any) {
	body := map[string]any{"error": code, "message": message}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, status, body)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		addonkit.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return false
	}
	return true
}

// caller is who makes the request: the administrator's account and web session as the kit
// confirmed them, the client's address and the way the request came in, as the system's web
// server says them. The headers are lighttpd's: it removes a client's own copies.
type caller struct {
	who       sessions.Who
	transport string // https, http, tunnel
}

// DevSID is the web session every caller has without the session check (development only); the
// fake system knows it as an administrator's.
const DevSID = "DEVDEVDEVDEVDEVDEVDEVDEVDE"

// Transport words of the state answer.
const (
	TransportHTTPS  = "https"
	TransportHTTP   = "http"
	TransportTunnel = "tunnel"
)

func (s *server) caller(r *http.Request) caller {
	c := caller{who: sessions.Who{User: "dev", SID: DevSID, Client: clientAddr(r)}, transport: TransportHTTP}
	if info := addonkit.FromRequest(r); info != nil {
		c.who.User, c.who.SID = info.User, info.ID
	}
	switch {
	case r.Header.Get("X-Occulite-Ingress") != "":
		c.transport = TransportTunnel
	case strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"):
		c.transport = TransportHTTPS
	}
	return c
}

// clientAddr is the client's address: the last element of X-Forwarded-For (the one lighttpd
// wrote), else the connection's.
func clientAddr(r *http.Request) string {
	vs := r.Header.Values("X-Forwarded-For")
	if len(vs) > 0 {
		parts := strings.Split(vs[len(vs)-1], ",")
		if a := strings.TrimSpace(parts[len(parts)-1]); a != "" {
			if ip := net.ParseIP(a); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// blocked says why a root session may not be opened or used over the way the request came in:
// "insecure-transport" over plain HTTP, "tunnel-refused" through a tunnel addon unless the
// administrator allowed it (a tunnel encrypts; the HTTPS rule is for the LAN). "" when it may.
func (s *server) blocked(c caller) string {
	switch c.transport {
	case TransportTunnel:
		if !s.o.Store.Settings().AllowTunnel {
			return "tunnel-refused"
		}
	case TransportHTTP:
		if !s.o.InsecureHTTP {
			return "insecure-transport"
		}
	}
	return ""
}

// fresh asks the system about the caller's web session now (not from the kit's cache): an
// administrator's, and whether the system runs without a login.
func (s *server) fresh(ctx context.Context, c caller) (admin, authOff bool, err error) {
	st, err := s.o.System.Session(ctx, c.who.SID, false)
	if err != nil {
		return false, false, err
	}
	return st.Admin(c.who.SID), st.AuthOff, nil
}

// GET /api/v1/health: {ok, version, uptime_s}.
func (s *server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "version": s.o.Version, "uptime_s": int(time.Since(s.o.Started).Seconds())})
}

type sessionView struct {
	sessions.Info
	// Mine says the session belongs to the caller's web session.
	Mine bool `json:"mine"`
}

func (s *server) views(c caller) []sessionView {
	list := s.o.Sessions.List()
	out := make([]sessionView, 0, len(list))
	for _, i := range list {
		out = append(out, sessionView{Info: i, Mine: i.Owner == c.who.SID})
	}
	return out
}

// GET /api/v1/state: everything the page shows.
func (s *server) state(w http.ResponseWriter, r *http.Request) {
	c := s.caller(r)
	sw, on := s.o.Core.Switch()
	_, authOff, _ := s.fresh(r.Context(), c)
	set := s.o.Store.Settings()
	out := map[string]any{
		"version":   s.o.Version,
		"now":       time.Now().UnixMilli(),
		"user":      c.who.User,
		"enabled":   on,
		"ssh":       s.o.Core.SSH(r.Context()),
		"transport": c.transport,
		"blocked":   s.blocked(c),
		"auth_off":  authOff,
		"settings":  set,
		"defaults":  config.Default,
		"bounds":    map[string]any{"min": config.Min, "max": config.Max},
		"windows":   config.Windows,
		"window":    config.DefaultWindow,
		"sessions":  s.views(c),
	}
	if on {
		out["by"] = sw.By
		if sw.Until != nil {
			out["until"] = sw.Until.UnixMilli()
		}
	}
	if p := s.o.Core.Peek(); p != nil {
		out["peek"] = *p
	}
	if until := s.o.Sessions.Locked(c.who); !until.IsZero() {
		out["locked_until"] = until.UnixMilli()
	}
	writeJSON(w, 200, out)
}

// PUT /api/v1/enabled {on, hours}: the switch. hours is 1, 8 or 0 (until switched off).
func (s *server) putEnabled(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On    bool `json:"on"`
		Hours *int `json:"hours"`
	}
	if !decode(w, r, &body) {
		return
	}
	c := s.caller(r)
	if !body.On {
		if err := s.o.Core.Disable(r.Context(), c.who.User, "user"); err != nil {
			addonkit.WriteError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		s.state(w, r)
		return
	}
	admin, authOff, err := s.fresh(r.Context(), c)
	if err != nil {
		addonkit.WriteError(w, http.StatusBadGateway, "system", "the system could not be asked about your session: "+err.Error())
		return
	}
	if !admin && !authOff {
		addonkit.WriteError(w, http.StatusForbidden, "forbidden", "administrators only")
		return
	}
	hours := config.DefaultWindow
	if body.Hours != nil {
		hours = *body.Hours
	}
	switch err := s.o.Core.Enable(r.Context(), c.who.User, hours, authOff); {
	case errors.Is(err, core.ErrAuthOff):
		addonkit.WriteError(w, http.StatusConflict, "auth-off", err.Error())
		return
	case errors.Is(err, core.ErrWindow):
		addonkit.WriteError(w, http.StatusUnprocessableEntity, "invalid", "hours is 1, 8 or 0")
		return
	case err != nil:
		addonkit.WriteError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.state(w, r)
}

// PUT /api/v1/settings: the limits and the tunnel switch.
func (s *server) putSettings(w http.ResponseWriter, r *http.Request) {
	var set config.Settings
	if !decode(w, r, &set) {
		return
	}
	old := s.o.Store.Settings()
	if err := s.o.Store.PutSettings(set); err != nil {
		var ve *config.ValidationError
		if errors.As(err, &ve) {
			addonkit.WriteError(w, http.StatusUnprocessableEntity, "invalid", err.Error())
			return
		}
		addonkit.WriteError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if old != set {
		c := s.caller(r)
		s.o.Audit.Printf("settings by=%s idle_minutes=%d max_hours=%d max_sessions=%d allow_tunnel=%t", c.who.User, set.IdleMinutes, set.MaxHours, set.MaxSessions, set.AllowTunnel)
		s.hub.ping()
	}
	s.state(w, r)
}

// POST /api/v1/sessions {password, cols, rows}: signs in to sshd as root and opens a session.
// 201 {session, handle}; the handle is the tab's secret for the terminal's WebSocket.
func (s *server) openSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
		Cols     int    `json:"cols"`
		Rows     int    `json:"rows"`
	}
	if !decode(w, r, &body) {
		return
	}
	c := s.caller(r)
	if _, on := s.o.Core.Switch(); !on {
		fail(w, http.StatusConflict, "disabled", "the addon is switched off", nil)
		return
	}
	if why := s.blocked(c); why != "" {
		fail(w, http.StatusForbidden, why, transportMessage(why), nil)
		return
	}
	admin, authOff, err := s.fresh(r.Context(), c)
	switch {
	case err != nil:
		fail(w, http.StatusBadGateway, "system", "the system could not be asked about your session: "+err.Error(), nil)
		return
	case authOff:
		fail(w, http.StatusConflict, "auth-off", core.ErrAuthOff.Error(), nil)
		return
	case !admin:
		fail(w, http.StatusForbidden, "forbidden", "administrators only", nil)
		return
	}
	if body.Password == "" || len(body.Password) > 1024 {
		fail(w, http.StatusUnprocessableEntity, "invalid", "root's password is needed", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	info, handle, err := s.o.Sessions.Open(ctx, c.who, body.Password, sshc.Size{Cols: body.Cols, Rows: body.Rows})
	body.Password = ""
	var locked *sessions.LockedError
	var many *sessions.TooManyError
	var wrong *sessions.WrongPasswordError
	switch {
	case err == nil:
	case errors.As(err, &locked):
		fail(w, http.StatusTooManyRequests, "locked", "too many failed attempts", map[string]any{"until": locked.Until.UnixMilli()})
		return
	case errors.As(err, &many):
		// who holds them: the account and since when, not the handles
		fail(w, http.StatusConflict, "too-many-sessions", "too many sessions are open", map[string]any{"sessions": many.Sessions})
		return
	case errors.As(err, &wrong):
		extra := map[string]any{"tries_left": wrong.TriesLeft}
		if until := s.o.Sessions.Locked(c.who); !until.IsZero() {
			extra["until"] = until.UnixMilli()
		}
		fail(w, http.StatusForbidden, "wrong-password", "wrong password", extra)
		return
	case errors.Is(err, sshc.ErrOff):
		fail(w, http.StatusConflict, "ssh-off", "SSH is switched off", nil)
		return
	case errors.Is(err, sshc.ErrKeysOnly):
		fail(w, http.StatusConflict, "keys-only", "SSH accepts keys only", nil)
		return
	case errors.Is(err, sshc.ErrHostKey):
		fail(w, http.StatusBadGateway, "host-key", err.Error(), nil)
		return
	default:
		s.o.Log("a sign-in failed: %v", err)
		fail(w, http.StatusBadGateway, "ssh", "the SSH connection failed: "+err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"session": sessionView{Info: info, Mine: true}, "handle": handle, "now": time.Now().UnixMilli()})
}

func transportMessage(why string) string {
	if why == "tunnel-refused" {
		return "the shell is not offered through a tunnel addon"
	}
	return "the shell works over HTTPS only"
}

// DELETE /api/v1/sessions/{id}: ends a session - one's own or, as an administrator, anyone's.
func (s *server) endSession(w http.ResponseWriter, r *http.Request) {
	if !s.o.Sessions.End(r.PathValue("id"), sessions.ReasonEnded) {
		addonkit.WriteError(w, http.StatusNotFound, "not-found", "no such session")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// DELETE /api/v1/sessions: ends every session.
func (s *server) endAll(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "ended": s.o.Sessions.EndAll(sessions.ReasonEnded)})
}

// GET /api/v1/events: Server-Sent Events; "change" whenever the switch, SSH's state or the
// session table changed (the page then reads /state), a comment every 15 s.
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		addonkit.WriteError(w, http.StatusInternalServerError, "internal", "no streaming")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	_, _ = w.Write([]byte(": connected\n\n"))
	fl.Flush()
	ch := s.hub.sub()
	defer s.hub.unsub(ch)
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			if _, err := w.Write([]byte("event: change\ndata: {}\n\n")); err != nil {
				return
			}
		case <-t.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
		}
		fl.Flush()
	}
}

// GET /api/v1/audit: the addon's audit lines since its start, newest first.
func (s *server) audit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.o.Audit.List())
}
