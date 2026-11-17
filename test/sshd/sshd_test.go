// Package sshd tests the addon end to end against a real OpenSSH sshd in docker
// (test/sshd/Dockerfile): the daemon's whole path - the API behind the kit's session check, the
// terminal's WebSocket, the SSH client - with a real root shell on a real pty at the other end.
// Run with SHELL_DOCKER=1 (CI does; without it the test is skipped).
package sshd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hobbyquaker/ccu-addon-kit/go/addonkit"

	"github.com/openccu-lite/lite-addon-shell/internal/audit"
	"github.com/openccu-lite/lite-addon-shell/internal/config"
	"github.com/openccu-lite/lite-addon-shell/internal/core"
	"github.com/openccu-lite/lite-addon-shell/internal/fakesys"
	"github.com/openccu-lite/lite-addon-shell/internal/httpapi"
	"github.com/openccu-lite/lite-addon-shell/internal/sessions"
	"github.com/openccu-lite/lite-addon-shell/internal/sshc"
	"github.com/openccu-lite/lite-addon-shell/internal/system"
)

const (
	image    = "lite-addon-shell-sshd:test"
	password = "e2e-root-pass"
	sid      = "ADMINADMINADMINADMINADMIN2"
)

func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

type stack struct {
	t     *testing.T
	srv   *httptest.Server
	ssh   *sshc.Client
	mgr   *sessions.Manager
	core  *core.Core
	audit *audit.Log
	sys   *fakesys.Server
}

// newStack is the daemon as cmd/lite-shell wires it, against sshd at addr.
func newStack(t *testing.T, addr, hostKeyDir string) *stack {
	s := &stack{t: t, audit: &audit.Log{}}
	dir := t.TempDir()
	s.sys = fakesys.New(sid, "admin")
	sysSrv := httptest.NewServer(s.sys.Handler())
	t.Cleanup(sysSrv.Close)
	store, _ := config.Open(filepath.Join(dir, "conf"))
	www := filepath.Join(dir, "www")
	_ = os.MkdirAll(www, 0o755)
	_ = os.WriteFile(filepath.Join(www, "index.html"), []byte("<!doctype html>"), 0o644)
	s.ssh = &sshc.Client{Addr: addr, User: "root", HostKeyGlobs: []string{filepath.Join(hostKeyDir, "ssh_host_*_key.pub")}, Timeout: 10 * time.Second}
	sys := &system.Client{Base: sysSrv.URL}
	s.mgr = sessions.New(sessions.Options{
		Open: func(ctx context.Context, pw string, size sshc.Size) (sessions.Shell, error) {
			sh, err := s.ssh.Open(ctx, pw, size)
			if err != nil {
				return nil, err
			}
			return sh, nil
		},
		Limits: func() sessions.Limits {
			c := store.Settings()
			return sessions.Limits{Idle: c.Idle(), Longest: c.Longest(), MaxSessions: c.MaxSessions}
		},
		Audit: s.audit.Printf,
	})
	t.Cleanup(func() { s.mgr.EndAll(sessions.ReasonStopping) })
	s.core = core.New(core.Options{AddonID: "shell", Store: store, Sessions: s.mgr, Probe: s.ssh.Probe, System: sys, Audit: s.audit.Printf})
	ch := addonkit.NewChecker(addonkit.SessionOptions{Base: sysSrv.URL, Need: addonkit.LevelAdmin, CacheTTL: -1,
		Platform: func() addonkit.Kind { return addonkit.OpenccuLite }})
	h, _ := httpapi.New(httpapi.Options{BasePath: "/addons/shell", WWW: www, Store: store, Core: s.core, Sessions: s.mgr, System: sys,
		Audit: s.audit, Wrap: ch.Handler, Version: "e2e", Started: time.Now()})
	s.srv = httptest.NewServer(h)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *stack) req(method, path string, body any) (int, map[string]any) {
	s.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r, _ := http.NewRequest(method, s.srv.URL+"/addons/shell/"+path, rd)
	r.Header.Set("X-Forwarded-For", "192.0.2.10")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Occulite-Session", sid)
	r.Header.Set("X-Occulite-Auth", "session")
	r.Header.Set("X-CCU-Addon-Request", "1")
	r.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

type term struct {
	t  *testing.T
	ws *websocket.Conn
	// everything the terminal showed, and the control messages
	out bytes.Buffer
	ctl []map[string]any
}

func (s *stack) terminal(cols, rows int) (*term, string) {
	s.t.Helper()
	code, out := s.req("POST", "api/v1/sessions", map[string]any{"password": password, "cols": cols, "rows": rows})
	if code != 201 {
		s.t.Fatalf("sign-in: %d %v", code, out)
	}
	id := out["session"].(map[string]any)["id"].(string)
	h := http.Header{}
	h.Set("X-Forwarded-For", "192.0.2.10")
	h.Set("X-Forwarded-Proto", "https")
	h.Set("X-Occulite-Session", sid)
	h.Set("X-Occulite-Auth", "session")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(s.srv.URL, "http")+"/addons/shell/api/v1/sessions/"+id+"/tty", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		s.t.Fatal(err)
	}
	ws.SetReadLimit(16 << 20)
	s.t.Cleanup(func() { ws.CloseNow() })
	hello, _ := json.Marshal(map[string]any{"handle": out["handle"], "cols": cols, "rows": rows})
	if err := ws.Write(ctx, websocket.MessageText, hello); err != nil {
		s.t.Fatal(err)
	}
	return &term{t: s.t, ws: ws}, id
}

func (c *term) send(s string) {
	c.t.Helper()
	if err := c.ws.Write(context.Background(), websocket.MessageBinary, []byte(s)); err != nil {
		c.t.Fatal(err)
	}
}

// expect reads until the output since the last expect matches re, and answers the match.
func (c *term) expect(re string, within time.Duration) []string {
	c.t.Helper()
	rx := regexp.MustCompile(re)
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	for {
		if m := rx.FindStringSubmatch(c.out.String()); m != nil {
			c.out.Reset()
			return m
		}
		typ, data, err := c.ws.Read(ctx)
		if err != nil {
			c.t.Fatalf("no %s in %q (%v)", re, tail(c.out.String()), err)
		}
		if typ == websocket.MessageBinary {
			c.out.Write(data)
		} else {
			var m map[string]any
			_ = json.Unmarshal(data, &m)
			c.ctl = append(c.ctl, m)
		}
	}
}

// closed reads until the socket closes and answers the close code and the end message's reason.
func (c *term) closed(within time.Duration) (websocket.StatusCode, string) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	reason := ""
	for {
		typ, data, err := c.ws.Read(ctx)
		if err != nil {
			return websocket.CloseStatus(err), reason
		}
		if typ == websocket.MessageText {
			var m map[string]any
			_ = json.Unmarshal(data, &m)
			if m["t"] == "end" {
				reason, _ = m["reason"].(string)
			}
		}
	}
}

func tail(s string) string {
	if len(s) > 400 {
		return "…" + s[len(s)-400:]
	}
	return s
}

func TestAgainstRealSSHD(t *testing.T) {
	if os.Getenv("SHELL_DOCKER") == "" {
		t.Skip("SHELL_DOCKER not set")
	}
	run(t, "docker", "build", "-q", "-t", image, ".")
	name := fmt.Sprintf("lite-addon-shell-sshd-%d", time.Now().UnixNano())
	run(t, "docker", "run", "-d", "--rm", "--name", name, "-p", "127.0.0.1:0:22", image)
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := exec.Command("docker", "logs", name).CombinedOutput()
			t.Logf("sshd's log:\n%s", tail(string(out)))
		}
		_ = exec.Command("docker", "rm", "-f", name).Run()
	})
	var port int
	for _, l := range strings.Split(run(t, "docker", "port", name, "22/tcp"), "\n") {
		if i := strings.LastIndex(l, ":"); i >= 0 {
			fmt.Sscanf(l[i+1:], "%d", &port)
		}
	}
	if port == 0 {
		t.Fatal("no port")
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	// the system's host key files, as the daemon reads them from /etc/ssh
	keys := t.TempDir()
	for _, kind := range []string{"ed25519", "rsa", "ecdsa"} {
		pub := run(t, "docker", "exec", name, "cat", "/etc/ssh/ssh_host_"+kind+"_key.pub")
		if err := os.WriteFile(filepath.Join(keys, "ssh_host_"+kind+"_key.pub"), []byte(pub+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := newStack(t, addr, keys)
	ctx := context.Background()
	deadline := time.Now().Add(30 * time.Second)
	for s.ssh.Probe(ctx) != sshc.StateOn {
		if time.Now().After(deadline) {
			t.Fatalf("sshd does not answer: %s", s.ssh.Probe(ctx))
		}
		time.Sleep(300 * time.Millisecond)
	}
	if code, out := s.req("PUT", "api/v1/enabled", map[string]any{"on": true, "hours": 1}); code != 200 {
		t.Fatalf("enable: %d %v", code, out)
	}

	t.Run("a wrong password is refused by sshd and counted", func(t *testing.T) {
		code, out := s.req("POST", "api/v1/sessions", map[string]any{"password": "not-it"})
		if code != 403 || out["error"] != "wrong-password" || out["tries_left"] != float64(4) {
			t.Fatalf("%d %v", code, out)
		}
	})

	t.Run("a foreign host key is refused before the password is sent", func(t *testing.T) {
		other := t.TempDir()
		_ = os.WriteFile(filepath.Join(other, "ssh_host_ed25519_key.pub"), []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBVQ4yyXXpWpbUMqZnHYRKX2rAeFl2S0GBeNv0Dpfs5V other\n"), 0o644)
		c := *s.ssh
		c.HostKeyGlobs = []string{filepath.Join(other, "*.pub")}
		if _, err := c.Open(ctx, password, sshc.Size{}); !errors.Is(err, sshc.ErrHostKey) {
			t.Fatalf("%v", err)
		}
		if st := c.Probe(ctx); st != sshc.StateUnknown {
			t.Fatalf("probe: %s", st)
		}
	})

	t.Run("a root shell on a real pty: id, the terminal's size, a resize, exit", func(t *testing.T) {
		c, id := s.terminal(100, 30)
		c.send("id -u; tty; echo TERM=$TERM\r")
		c.expect(`(?s)\n0\r\n/dev/pts/\d+\r\nTERM=xterm-256color`, 15*time.Second)
		c.send("stty size\r")
		c.expect(`\n30 100\r\n`, 10*time.Second)
		if err := c.ws.Write(ctx, websocket.MessageText, []byte(`{"t":"resize","cols":132,"rows":43}`)); err != nil {
			t.Fatal(err)
		}
		c.send("sleep 0.5; stty size\r")
		c.expect(`\n43 132\r\n`, 10*time.Second)
		// sshd's own view: one root login from the loopback's side, by password
		who := run(t, "docker", "exec", name, "sh", "-c", "ps -o user,args | grep -c '[s]shd.*root@pts' || true")
		if who == "0" {
			t.Errorf("sshd shows no root session on a pty")
		}
		c.send("exit\r")
		if code, reason := c.closed(10 * time.Second); code != 4000 || reason != sessions.ReasonExit {
			t.Fatalf("after exit: %d %q", code, reason)
		}
		lines, _ := json.Marshal(s.audit.List())
		for _, want := range []string{"session-open kind=terminal user=admin client=192.0.2.10 id=" + id + " auth=password", "session-close id=" + id + " user=admin reason=exit"} {
			if !strings.Contains(string(lines), want) {
				t.Errorf("audit lacks %q", want)
			}
		}
		if strings.Contains(string(lines), password) || strings.Contains(string(lines), "stty") {
			t.Errorf("the audit holds the password or what was typed")
		}
	})

	t.Run("a flood of output arrives whole and in order", func(t *testing.T) {
		c, id := s.terminal(120, 30)
		// 300000 numbered lines, about 2 MB; the end marker is computed, so the typed line's echo is not it
		c.send("stty -echo; seq 1 300000; echo FLOOD-$((1+1))END\r")
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		var all bytes.Buffer
		for !bytes.Contains(all.Bytes(), []byte("FLOOD-2END")) {
			typ, data, err := c.ws.Read(ctx)
			if err != nil {
				t.Fatalf("after %d bytes: %v", all.Len(), err)
			}
			if typ == websocket.MessageBinary {
				all.Write(data)
			}
		}
		text := all.String()
		for _, n := range []string{"\n1\r\n", "\n150000\r\n", "\n299999\r\n300000\r\nFLOOD-2END"} {
			if !strings.Contains(text, n) {
				t.Errorf("the output lacks %q", n)
			}
		}
		if got := strings.Count(text, "\r\n"); got < 300000 {
			t.Errorf("%d lines arrived", got)
		}
		s.mgr.End(id, sessions.ReasonEnded)
	})

	t.Run("keys only: the probe says so and root's password is not even tried", func(t *testing.T) {
		run(t, "docker", "exec", name, "sh", "-c", "echo 'PasswordAuthentication no' > /etc/ssh/sshd_config.d/keys.conf && kill -HUP 1")
		defer func() {
			run(t, "docker", "exec", name, "sh", "-c", "rm /etc/ssh/sshd_config.d/keys.conf && kill -HUP 1")
			for i := 0; i < 50 && s.ssh.Probe(ctx) != sshc.StateOn; i++ {
				time.Sleep(200 * time.Millisecond)
			}
		}()
		deadline := time.Now().Add(15 * time.Second)
		for s.ssh.Probe(ctx) != sshc.StateKeysOnly {
			if time.Now().After(deadline) {
				t.Fatalf("probe: %s", s.ssh.Probe(ctx))
			}
			time.Sleep(200 * time.Millisecond)
		}
		time.Sleep(5100 * time.Millisecond) // the state's probe is kept for five seconds
		if code, out := s.req("GET", "api/v1/state", nil); code != 200 || out["ssh"] != "keys-only" {
			t.Fatalf("state: %d %v", code, out["ssh"])
		}
		if code, out := s.req("POST", "api/v1/sessions", map[string]any{"password": password}); code != 409 || out["error"] != "keys-only" {
			t.Fatalf("sign-in: %d %v", code, out)
		}
	})

	t.Run("sshd going away mid-session ends the session as a lost connection", func(t *testing.T) {
		if st := s.ssh.Probe(ctx); st != sshc.StateOn {
			t.Fatalf("sshd is %s", st)
		}
		c, _ := s.terminal(80, 24)
		c.send("echo ready-$((40+2))\r")
		c.expect(`ready-42`, 10*time.Second)
		run(t, "docker", "kill", name)
		code, reason := c.closed(40 * time.Second)
		if code != 4000 || reason != sessions.ReasonConnection {
			t.Fatalf("after sshd went: %d %q", code, reason)
		}
		if st := s.ssh.Probe(ctx); st != sshc.StateOff {
			t.Fatalf("probe: %s", st)
		}
		if code, out := s.req("POST", "api/v1/sessions", map[string]any{"password": password}); code != 409 || out["error"] != "ssh-off" {
			t.Fatalf("sign-in without sshd: %d %v", code, out)
		}
	})
}
