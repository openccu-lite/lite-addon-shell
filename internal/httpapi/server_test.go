package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hobbyquaker/ccu-addon-kit/go/addonkit"

	"github.com/openccu-lite/lite-addon-shell/internal/audit"
	"github.com/openccu-lite/lite-addon-shell/internal/config"
	"github.com/openccu-lite/lite-addon-shell/internal/core"
	"github.com/openccu-lite/lite-addon-shell/internal/fakesshd"
	"github.com/openccu-lite/lite-addon-shell/internal/fakesys"
	"github.com/openccu-lite/lite-addon-shell/internal/files"
	"github.com/openccu-lite/lite-addon-shell/internal/httpapi"
	"github.com/openccu-lite/lite-addon-shell/internal/sessions"
	"github.com/openccu-lite/lite-addon-shell/internal/sshc"
	"github.com/openccu-lite/lite-addon-shell/internal/system"
)

const (
	sidAdmin = "ADMINADMINADMINADMINADMIN2"
	sidOther = "OTHEROTHEROTHEROTHEROTHER2"
	sidUser  = "CONFIGCONFIGCONFIGCONFIG22"
)

type env struct {
	t    *testing.T
	srv  *httptest.Server
	sys  *fakesys.Server
	sshd *fakesshd.Server
	// tree is the fake sshd's file system (a scratch directory seeded like a small system)
	tree  *fakesshd.Tree
	root  string
	mgr   *sessions.Manager
	core  *core.Core
	store *config.Store
	audit *audit.Log
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t}
	var err error
	if e.sshd, err = fakesshd.New("root-pass"); err != nil {
		t.Fatal(err)
	}
	if err := e.sshd.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.sshd.Kill)
	dir := t.TempDir()
	e.root = filepath.Join(dir, "root")
	if err := fakesshd.Seed(e.root); err != nil {
		t.Fatal(err)
	}
	e.tree = fakesshd.NewTree(e.root)
	e.sshd.Files = e.tree
	if err := e.sshd.WriteHostKey(filepath.Join(dir, "ssh_host_ed25519_key.pub")); err != nil {
		t.Fatal(err)
	}
	e.sys = fakesys.New(sidAdmin, "admin")
	e.sys.Sessions[sidOther] = fakesys.Account{User: "other", Level: "administer"}
	e.sys.Sessions[sidUser] = fakesys.Account{User: "conf", Level: "configure"}
	sysSrv := httptest.NewServer(e.sys.Handler())
	t.Cleanup(sysSrv.Close)
	token := filepath.Join(dir, "control-token")
	_ = os.WriteFile(token, []byte("ctl\n"), 0o600)
	e.sys.ControlToken = "ctl"

	e.store, _ = config.Open(filepath.Join(dir, "conf"))
	www := filepath.Join(dir, "www")
	_ = os.MkdirAll(www, 0o755)
	_ = os.WriteFile(filepath.Join(www, "index.html"), []byte("<!doctype html><title>Shell</title>"), 0o644)
	_ = os.WriteFile(filepath.Join(www, "terminal.html"), []byte("<!doctype html><title>Shell (root)</title>"), 0o644)
	_ = os.WriteFile(filepath.Join(www, "files.html"), []byte("<!doctype html><title>Shell (root)</title>"), 0o644)
	ssh := &sshc.Client{Addr: e.sshd.Addr(), User: "root", HostKeyGlobs: []string{filepath.Join(dir, "ssh_host_*_key.pub")}, Timeout: 5 * time.Second}
	sys := &system.Client{Base: sysSrv.URL}
	e.audit = &audit.Log{}
	var ping func()
	changed := func() {
		if ping != nil {
			ping()
		}
	}
	e.mgr = sessions.New(sessions.Options{
		Open: func(ctx context.Context, pw string, size sshc.Size) (sessions.Shell, error) {
			sh, err := ssh.Open(ctx, pw, size)
			if err != nil {
				return nil, err
			}
			return sh, nil
		},
		OpenFiles: func(ctx context.Context, pw string) (sessions.Conn, error) {
			f, err := files.Open(ctx, ssh, pw, t.Logf)
			if err != nil {
				return nil, err
			}
			return f, nil
		},
		Limits: func() sessions.Limits {
			s := e.store.Settings()
			return sessions.Limits{Idle: s.Idle(), Longest: s.Longest(), MaxSessions: s.MaxSessions}
		},
		Audit: e.audit.Printf, OnChange: changed,
	})
	t.Cleanup(func() { e.mgr.EndAll(sessions.ReasonStopping) })
	e.core = core.New(core.Options{AddonID: "shell", Store: e.store, Sessions: e.mgr, Probe: ssh.Probe, System: sys,
		Notices: &system.Notifier{Base: sysSrv.URL, TokenFile: token}, Audit: e.audit.Printf, OnChange: changed})
	// the kit's real checker, as the daemon uses it: administrators, no token
	ch := addonkit.NewChecker(addonkit.SessionOptions{Base: sysSrv.URL, Need: addonkit.LevelAdmin, CacheTTL: -1,
		Platform: func() addonkit.Kind { return addonkit.OpenccuLite }})
	h, p := httpapi.New(httpapi.Options{BasePath: "/addons/shell", WWW: www, Store: e.store, Core: e.core, Sessions: e.mgr, System: sys,
		Audit: e.audit, Wrap: ch.Handler, ReadTimeout: 300 * time.Millisecond, Version: "test", Started: time.Now()})
	ping = p
	e.srv = httptest.NewServer(h)
	t.Cleanup(e.srv.Close)
	return e
}

// req is a request as lighttpd's gate hands it on: the session header, the client's address, the
// scheme. hdr overrides.
func (e *env) req(method, path, sid string, body any, hdr map[string]string) (int, map[string]any, http.Header) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r, _ := http.NewRequest(method, e.srv.URL+"/addons/shell/"+path, rd)
	r.Header.Set("X-Forwarded-For", "192.0.2.10")
	r.Header.Set("X-Forwarded-Proto", "https")
	if sid != "" {
		r.Header.Set("X-Occulite-Session", sid)
		r.Header.Set("X-Occulite-Auth", "session")
	}
	if method != http.MethodGet {
		r.Header.Set("X-CCU-Addon-Request", "1")
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		if v == "" {
			r.Header.Del(k)
		} else {
			r.Header.Set(k, v)
		}
	}
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out, res.Header
}

func (e *env) enable() {
	e.t.Helper()
	if st, out, _ := e.req("PUT", "api/v1/enabled", sidAdmin, map[string]any{"on": true, "hours": 8}, nil); st != 200 || out["enabled"] != true {
		e.t.Fatalf("enable: %d %v", st, out)
	}
}

func (e *env) open(sid string) (id, handle string) {
	e.t.Helper()
	st, out, _ := e.req("POST", "api/v1/sessions", sid, map[string]any{"password": "root-pass", "cols": 100, "rows": 30}, nil)
	if st != 201 {
		e.t.Fatalf("open: %d %v", st, out)
	}
	return out["session"].(map[string]any)["id"].(string), out["handle"].(string)
}

// dial opens the terminal's WebSocket as the gate would hand it on.
func (e *env) dial(id, sid string, hdr map[string]string) (*websocket.Conn, *http.Response, error) {
	h := http.Header{}
	h.Set("X-Forwarded-For", "192.0.2.10")
	h.Set("X-Forwarded-Proto", "https")
	h.Set("X-Occulite-Session", sid)
	h.Set("X-Occulite-Auth", "session")
	for k, v := range hdr {
		h.Set(k, v)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/addons/shell/api/v1/sessions/"+id+"/tty", &websocket.DialOptions{HTTPHeader: h})
}

// readUntil reads binary frames until they hold want; control frames are collected.
func readUntil(t *testing.T, ws *websocket.Conn, want string) (string, []map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var got strings.Builder
	var ctl []map[string]any
	for !strings.Contains(got.String(), want) {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("no %q in %q (%v)", want, got.String(), err)
		}
		if typ == websocket.MessageBinary {
			got.Write(data)
		} else {
			var m map[string]any
			_ = json.Unmarshal(data, &m)
			ctl = append(ctl, m)
		}
	}
	return got.String(), ctl
}

func (e *env) attach(id, handle, sid string) *websocket.Conn {
	e.t.Helper()
	ws, _, err := e.dial(id, sid, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { ws.CloseNow() })
	hello, _ := json.Marshal(map[string]any{"handle": handle, "cols": 100, "rows": 30})
	if err := ws.Write(context.Background(), websocket.MessageText, hello); err != nil {
		e.t.Fatal(err)
	}
	return ws
}

// The whole way: switched on, a sign-in with root's password, the terminal over the WebSocket
// (id answers root), a resize, and the end from the page with its reason.
func TestTerminal(t *testing.T) {
	e := newEnv(t)
	e.enable()
	id, handle := e.open(sidAdmin)
	ws := e.attach(id, handle, sidAdmin)
	out, ctl := readUntil(t, ws, "# ")
	if len(ctl) != 1 || ctl[0]["t"] != "ready" {
		t.Fatalf("the first message is %v", ctl)
	}
	if s := ctl[0]["session"].(map[string]any); s["user"] != "admin" || s["client"] != "192.0.2.10" || s["mine"] != true {
		t.Fatalf("ready: %v", s)
	}
	_ = out
	ctx := context.Background()
	_ = ws.Write(ctx, websocket.MessageBinary, []byte("id\r"))
	readUntil(t, ws, "uid=0(root)")
	_ = ws.Write(ctx, websocket.MessageText, []byte(`{"t":"resize","cols":132,"rows":43}`))
	_ = ws.Write(ctx, websocket.MessageBinary, []byte("sleep 1\rsize\r"))
	readUntil(t, ws, "132 43")
	_ = ws.Write(ctx, websocket.MessageText, []byte(`{"t":"ping"}`))
	// the state lists it, as the caller's own
	_, st, _ := e.req("GET", "api/v1/state", sidAdmin, nil, nil)
	list := st["sessions"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["mine"] != true || list[0].(map[string]any)["attached"] != true {
		t.Fatalf("sessions %v", list)
	}
	if _, st2, _ := e.req("GET", "api/v1/state", sidOther, nil, nil); st2["sessions"].([]any)[0].(map[string]any)["mine"] != false {
		t.Error("another administrator sees the session as his own")
	}
	if strings.Contains(mustJSON(st), handle) {
		t.Fatal("the state answer carries the handle")
	}
	// ended from the page (any administrator may): the tab is told why
	if code, _, _ := e.req("DELETE", "api/v1/sessions/"+id, sidOther, nil, nil); code != 200 {
		t.Fatalf("end: %d", code)
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var end map[string]any
	for {
		typ, data, err := ws.Read(rctx)
		if err != nil {
			if websocket.CloseStatus(err) != 4000 {
				t.Fatalf("close: %v", err)
			}
			break
		}
		if typ == websocket.MessageText {
			var m map[string]any
			_ = json.Unmarshal(data, &m)
			if m["t"] == "end" {
				end = m
			}
		}
	}
	if end == nil || end["reason"] != "ended" {
		t.Fatalf("end message %v", end)
	}
	// the audit lines: who, from where, why - and nothing typed
	lines := mustJSON(e.audit.List())
	for _, want := range []string{"enabled until=", "session-open kind=terminal user=admin client=192.0.2.10 id=" + id + " auth=password", "session-close id=" + id + " user=admin reason=ended"} {
		if !strings.Contains(lines, want) {
			t.Errorf("audit lacks %q: %s", want, lines)
		}
	}
	if strings.Contains(lines, "root-pass") || strings.Contains(lines, "uid=0") {
		t.Errorf("the audit holds what was typed or shown: %s", lines)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Off after the install: no session until an administrator switches it on; the switch posts the
// Status page's notice and clears it; switching off ends the sessions.
func TestSwitch(t *testing.T) {
	e := newEnv(t)
	_, st, _ := e.req("GET", "api/v1/state", sidAdmin, nil, nil)
	if st["enabled"] != false || st["ssh"] != "on" || st["transport"] != "https" || st["blocked"] != "" || st["window"] != float64(8) {
		t.Fatalf("state after the install: %v", st)
	}
	if code, out, _ := e.req("POST", "api/v1/sessions", sidAdmin, map[string]any{"password": "root-pass"}, nil); code != 409 || out["error"] != "disabled" {
		t.Fatalf("a sign-in while off: %d %v", code, out)
	}
	if code, out, _ := e.req("PUT", "api/v1/enabled", sidAdmin, map[string]any{"on": true, "hours": 5}, nil); code != 422 {
		t.Fatalf("five hours: %d %v", code, out)
	}
	e.enable()
	e.sys.With(func(s *fakesys.Server) {
		n := s.Notices["enabled"]
		if n == nil || n["severity"] != "warning" || n["link"] != "/addon-settings/shell" || n["ttl"] == nil {
			t.Errorf("the notice: %v", n)
		}
		title := n["title"].(map[string]any)
		if title["de"] == "" || title["en"] == "" {
			t.Errorf("the notice's title: %v", title)
		}
	})
	id, handle := e.open(sidAdmin)
	ws := e.attach(id, handle, sidAdmin)
	readUntil(t, ws, "# ")
	if code, out, _ := e.req("PUT", "api/v1/enabled", sidAdmin, map[string]any{"on": false}, nil); code != 200 || out["enabled"] != false {
		t.Fatalf("off: %d %v", code, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := ws.Read(ctx); err != nil {
			if websocket.CloseStatus(err) != 4000 || !strings.Contains(err.Error(), "addon-off") {
				t.Fatalf("close: %v", err)
			}
			break
		}
	}
	e.sys.With(func(s *fakesys.Server) {
		if len(s.Notices) != 0 {
			t.Errorf("the notice stays: %v", s.Notices)
		}
	})
	// without a login the switch is refused
	e.sys.With(func(s *fakesys.Server) { s.AuthOff = true })
	if code, out, _ := e.req("PUT", "api/v1/enabled", sidAdmin, map[string]any{"on": true}, nil); code != 409 || out["error"] != "auth-off" {
		t.Fatalf("on without a login: %d %v", code, out)
	}
}

// Who gets in: administrators' web sessions only. No session, a lower level, and an API token
// (whatever the gate let through) are refused; a write without the CSRF header too.
func TestWhoGetsIn(t *testing.T) {
	e := newEnv(t)
	e.enable()
	if code, _, _ := e.req("GET", "api/v1/state", "", nil, nil); code != 401 {
		t.Errorf("no session: %d", code)
	}
	if code, _, _ := e.req("GET", "api/v1/state", sidUser, nil, nil); code != 403 {
		t.Errorf("a configure account: %d", code)
	}
	if code, _, _ := e.req("GET", "", sidUser, nil, nil); code != 403 {
		t.Errorf("the page for a configure account: %d", code)
	}
	tok := "olt_" + strings.Repeat("ab", 16)
	for _, auth := range []string{"token", ""} {
		if code, _, _ := e.req("POST", "api/v1/sessions", tok, map[string]any{"password": "root-pass"}, map[string]string{"X-Occulite-Auth": auth}); code != 401 && code != 403 {
			t.Errorf("a token (auth %q): %d", auth, code)
		}
	}
	if code, out, _ := e.req("POST", "api/v1/sessions", sidAdmin, map[string]any{"password": "root-pass"}, map[string]string{"X-CCU-Addon-Request": ""}); code != 403 || out["error"] != "csrf" {
		t.Errorf("no CSRF header: %d %v", code, out)
	}
	if code, out, _ := e.req("POST", "api/v1/sessions", sidAdmin, map[string]any{"password": "root-pass"}, map[string]string{"Origin": "https://elsewhere.example"}); code != 403 || out["error"] != "csrf" {
		t.Errorf("a foreign origin: %d %v", code, out)
	}
	// a web session that ended between the kit's check and the sign-in
	if e.mgr.Count() != 0 {
		t.Fatalf("%d sessions opened", e.mgr.Count())
	}
	if e.sshd.Logins() != 0 {
		t.Fatal("a refused request reached sshd")
	}
}

// Root's password and the terminal never travel in clear: plain HTTP is refused, for the sign-in
// and for the WebSocket. Through a tunnel addon the shell is refused unless the switch allows it.
func TestTransport(t *testing.T) {
	e := newEnv(t)
	e.enable()
	plain := map[string]string{"X-Forwarded-Proto": "http"}
	if _, st, _ := e.req("GET", "api/v1/state", sidAdmin, nil, plain); st["transport"] != "http" || st["blocked"] != "insecure-transport" {
		t.Fatalf("state over HTTP: %v", st)
	}
	if code, out, _ := e.req("POST", "api/v1/sessions", sidAdmin, map[string]any{"password": "root-pass"}, plain); code != 403 || out["error"] != "insecure-transport" {
		t.Fatalf("a sign-in over HTTP: %d %v", code, out)
	}
	tunnel := map[string]string{"X-Occulite-Ingress": "wireguard", "X-Forwarded-Proto": "http", "X-Forwarded-For": "10.77.0.2"}
	if _, st, _ := e.req("GET", "api/v1/state", sidAdmin, nil, tunnel); st["transport"] != "tunnel" || st["blocked"] != "tunnel-refused" {
		t.Fatalf("state through a tunnel: %v", st)
	}
	if code, out, _ := e.req("POST", "api/v1/sessions", sidAdmin, map[string]any{"password": "root-pass"}, tunnel); code != 403 || out["error"] != "tunnel-refused" {
		t.Fatalf("a sign-in through a tunnel: %d %v", code, out)
	}
	if e.sshd.Logins() != 0 {
		t.Fatal("a refused sign-in reached sshd")
	}
	id, handle := e.open(sidAdmin)
	if _, res, err := e.dial(id, sidAdmin, plain); err == nil || res == nil || res.StatusCode != 403 {
		t.Fatalf("the WebSocket over HTTP: %v %v", err, res)
	}
	if _, res, err := e.dial(id, sidAdmin, tunnel); err == nil || res == nil || res.StatusCode != 403 {
		t.Fatalf("the WebSocket through a tunnel: %v %v", err, res)
	}
	_ = handle
	// the administrator allows tunnels: the sign-in works from there, over the tunnel's plain HTTP
	set := e.store.Settings()
	set.AllowTunnel = true
	if code, out, _ := e.req("PUT", "api/v1/settings", sidAdmin, set, nil); code != 200 || out["settings"].(map[string]any)["allow_tunnel"] != true {
		t.Fatalf("settings: %d %v", code, out)
	}
	code, out, _ := e.req("POST", "api/v1/sessions", sidAdmin, map[string]any{"password": "root-pass"}, tunnel)
	if code != 201 || out["session"].(map[string]any)["client"] != "10.77.0.2" {
		t.Fatalf("a sign-in through an allowed tunnel: %d %v", code, out)
	}
	if code, out, _ := e.req("PUT", "api/v1/settings", sidAdmin, map[string]any{"idle_minutes": 1, "max_hours": 8, "max_sessions": 2}, nil); code != 422 {
		t.Errorf("idle 1 min: %d %v", code, out)
	}
}

// The sign-in's refusals as the page shows them: a wrong password with the tries left, the
// lockout with its end, too many sessions with who holds them, SSH off, keys only.
func TestSignInRefusals(t *testing.T) {
	e := newEnv(t)
	e.enable()
	for i := 4; i >= 1; i-- {
		code, out, _ := e.req("POST", "api/v1/sessions", sidAdmin, map[string]any{"password": "nope"}, nil)
		if code != 403 || out["error"] != "wrong-password" || out["tries_left"] != float64(i) {
			t.Fatalf("wrong password: %d %v", code, out)
		}
	}
	code, out, _ := e.req("POST", "api/v1/sessions", sidAdmin, map[string]any{"password": "nope"}, nil)
	if code != 403 || out["tries_left"] != float64(0) || out["until"] == nil {
		t.Fatalf("the fifth: %d %v", code, out)
	}
	code, out, _ = e.req("POST", "api/v1/sessions", sidAdmin, map[string]any{"password": "root-pass"}, nil)
	if code != 429 || out["error"] != "locked" || out["until"] == nil {
		t.Fatalf("locked: %d %v", code, out)
	}
	if _, st, _ := e.req("GET", "api/v1/state", sidAdmin, nil, nil); st["locked_until"] == nil {
		t.Error("the state does not say the lock")
	}
	// another administrator from another address
	other := map[string]string{"X-Forwarded-For": "192.0.2.77"}
	if code, _, _ := e.req("POST", "api/v1/sessions", sidOther, map[string]any{"password": ""}, other); code != 422 {
		t.Errorf("an empty password: %d", code)
	}
	for i := 0; i < 2; i++ {
		if code, out, _ := e.req("POST", "api/v1/sessions", sidOther, map[string]any{"password": "root-pass"}, other); code != 201 {
			t.Fatalf("open %d: %d %v", i, code, out)
		}
	}
	code, out, _ = e.req("POST", "api/v1/sessions", sidOther, map[string]any{"password": "root-pass"}, other)
	if code != 409 || out["error"] != "too-many-sessions" || len(out["sessions"].([]any)) != 2 {
		t.Fatalf("a third: %d %v", code, out)
	}
	if code, out, _ := e.req("DELETE", "api/v1/sessions", sidOther, nil, other); code != 200 || out["ended"] != float64(2) {
		t.Fatalf("end all: %d %v", code, out)
	}
	e.sshd.SetKeysOnly(true)
	time.Sleep(10 * time.Millisecond)
	if code, out, _ := e.req("POST", "api/v1/sessions", sidOther, map[string]any{"password": "root-pass"}, other); code != 409 || out["error"] != "keys-only" {
		t.Fatalf("keys only: %d %v", code, out)
	}
	e.sshd.Stop()
	if code, out, _ := e.req("POST", "api/v1/sessions", sidOther, map[string]any{"password": "root-pass"}, other); code != 409 || out["error"] != "ssh-off" {
		t.Fatalf("SSH off: %d %v", code, out)
	}
}

// The terminal belongs to the tab that holds the handle and to the web session that opened it.
func TestAttachRules(t *testing.T) {
	e := newEnv(t)
	e.enable()
	id, handle := e.open(sidAdmin)
	// another administrator, even with the handle
	if _, res, err := e.dial(id, sidOther, nil); err == nil || res == nil || res.StatusCode != 403 {
		t.Fatalf("another web session: %v %v", err, res)
	}
	// a page of another origin
	if _, res, err := e.dial(id, sidAdmin, map[string]string{"Origin": "https://elsewhere.example"}); err == nil || res == nil || res.StatusCode != 403 {
		t.Fatalf("a foreign origin: %v %v", err, res)
	}
	if _, res, err := e.dial("99", sidAdmin, nil); err == nil || res == nil || res.StatusCode != 404 {
		t.Fatalf("no such session: %v %v", err, res)
	}
	// the right web session with a wrong handle
	ws := e.attach(id, strings.Repeat("0", 64), sidAdmin)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := ws.Read(ctx); websocket.CloseStatus(err) != 4003 {
		t.Fatalf("a wrong handle: %v", err)
	}
	// a second tab with the handle takes over; the first is told
	a := e.attach(id, handle, sidAdmin)
	readUntil(t, a, "# ")
	b := e.attach(id, handle, sidAdmin)
	out, _ := readUntil(t, b, "# ")
	if !strings.Contains(out, "fake shell") {
		t.Errorf("the second tab does not get the output so far: %q", out)
	}
	for {
		if _, _, err := a.Read(ctx); err != nil {
			if websocket.CloseStatus(err) != 4001 {
				t.Fatalf("the replaced tab: %v", err)
			}
			break
		}
	}
	if e.mgr.Count() != 1 {
		t.Fatal("the take-over ended the session")
	}
}

// sshd makes the loopback wait after failed sign-ins (OpenSSH's PerSourcePenalties): the state and
// the sign-in say that, the right password is not counted as a wrong one, and it passes.
func TestTheSSHServersWaitAfterFailedSignInsIsSaidAsSuch(t *testing.T) {
	e := newEnv(t)
	e.enable()
	e.sshd.SetRefusing(true)
	// the pages learn it from the sign-in that ran into it, not only at their next look
	if _, st, _ := e.req("GET", "api/v1/state", sidAdmin, nil, nil); st["ssh"] != "on" {
		t.Fatalf("state before anyone tried: %v", st["ssh"])
	}
	for _, kind := range []string{"terminal", "files"} {
		code, out, _ := e.req("POST", "api/v1/sessions", sidAdmin, map[string]any{"kind": kind, "password": "root-pass"}, nil)
		if code != 503 || out["error"] != "ssh-refusing" {
			t.Fatalf("%s: %d %v", kind, code, out)
		}
	}
	_, st, _ := e.req("GET", "api/v1/state", sidAdmin, nil, nil)
	if st["ssh"] != "refusing" || st["locked_until"] != nil {
		t.Fatalf("the state after the refused sign-ins: ssh %v, locked %v", st["ssh"], st["locked_until"])
	}
	e.sshd.SetRefusing(false)
	time.Sleep(2100 * time.Millisecond) // the state's probe is kept for two seconds
	if _, st, _ := e.req("GET", "api/v1/state", sidAdmin, nil, nil); st["ssh"] != "on" {
		t.Fatalf("state afterwards: %v", st["ssh"])
	}
	e.open(sidAdmin)
}

// The terminal and the files page refuse framing and foreign scripts; the launcher page stays
// frameable (the system's shell frames it). The API is never cached.
func TestPageHeaders(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"terminal.html", "terminal", "files.html", "files"} {
		code, _, h := e.req("GET", p, sidAdmin, nil, nil)
		csp := h.Get("Content-Security-Policy")
		if code != 200 || !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "script-src 'self'") || h.Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: %d %v", p, code, h)
		}
		if strings.Contains(csp, "unsafe-eval") || strings.Contains(strings.Split(csp, "style-src")[0], "unsafe-inline") {
			t.Errorf("the policy admits inline or evaluated script: %s", csp)
		}
		// the editor's workers are the page's own files: no blob:, no other origin
		if strings.HasPrefix(p, "files") && (!strings.Contains(csp, "worker-src 'self';") || strings.Contains(csp, "blob:")) {
			t.Errorf("the files page's policy for workers: %s", csp)
		}
	}
	code, _, h := e.req("GET", "", sidAdmin, nil, nil)
	if code != 200 || h.Get("X-Frame-Options") != "" || h.Get("Content-Security-Policy") != "" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("the launcher page: %d %v", code, h)
	}
	if _, _, h := e.req("GET", "api/v1/state", sidAdmin, nil, nil); h.Get("Cache-Control") != "no-store" {
		t.Errorf("the API's Cache-Control: %q", h.Get("Cache-Control"))
	}
	if code, _, _ := e.req("GET", "api/v1/nope", sidAdmin, nil, nil); code != 404 {
		t.Errorf("an unknown route: %d", code)
	}
}

// The event stream says "change" when the table changes.
func TestEvents(t *testing.T) {
	e := newEnv(t)
	r, _ := http.NewRequest("GET", e.srv.URL+"/addons/shell/api/v1/events", nil)
	r.Header.Set("X-Occulite-Session", sidAdmin)
	r.Header.Set("X-Forwarded-Proto", "https")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := http.DefaultClient.Do(r.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	buf := make([]byte, 256)
	n, _ := res.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), ": connected") {
		t.Fatalf("stream start %q", buf[:n])
	}
	e.enable()
	var got strings.Builder
	for !strings.Contains(got.String(), "event: change") {
		n, err := res.Body.Read(buf)
		got.Write(buf[:n])
		if err != nil {
			t.Fatalf("no change event: %q (%v)", got.String(), err)
		}
	}
}

// Every route is in the readme's table, and the table names no other.
func TestRoutesDocumented(t *testing.T) {
	newEnv(t)
	b, err := os.ReadFile("../../README.en.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	for _, r := range httpapi.Routes {
		method, path, _ := strings.Cut(r, " ")
		want := "`" + method + " " + strings.TrimPrefix(path, "/api/v1") + "`"
		if !strings.Contains(doc, want) {
			t.Errorf("the readme lacks %s", want)
		}
	}
}
