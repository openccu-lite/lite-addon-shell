// Package sshd tests the addon end to end against a real OpenSSH sshd in docker
// (test/sshd/Dockerfile): the daemon's whole path - the API behind the kit's session check, the
// terminal's WebSocket, the SSH client - with a real root shell on a real pty at the other end,
// and the file API with OpenSSH's own SFTP server, as root, on a real kernel's mounts: a
// read-only one, a full one, and a bind-mounted file that cannot be replaced by a rename.
// Run with SHELL_DOCKER=1 (CI does; without it the test is skipped).
package sshd

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hobbyquaker/ccu-addon-kit/go/addonkit"
	"golang.org/x/crypto/ssh"

	"github.com/openccu-lite/lite-addon-shell/internal/audit"
	"github.com/openccu-lite/lite-addon-shell/internal/config"
	"github.com/openccu-lite/lite-addon-shell/internal/core"
	"github.com/openccu-lite/lite-addon-shell/internal/fakesys"
	"github.com/openccu-lite/lite-addon-shell/internal/files"
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
		OpenFiles: func(ctx context.Context, pw string) (sessions.Conn, error) {
			f, err := files.Open(ctx, s.ssh, pw, t.Logf)
			if err != nil {
				return nil, err
			}
			return f, nil
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

// fs is a request for a files session's files, as the gate hands it on, with the handle in its
// header; body is sent as it is. It answers the status, the body and the headers.
func (s *stack) fs(method, id, handle, call string, body []byte, hdr map[string]string) (int, []byte, http.Header) {
	s.t.Helper()
	r, _ := http.NewRequest(method, s.srv.URL+"/addons/shell/api/v1/sessions/"+id+"/fs/"+call, bytes.NewReader(body))
	r.Header.Set("X-Forwarded-For", "192.0.2.10")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Occulite-Session", sid)
	r.Header.Set("X-Occulite-Auth", "session")
	r.Header.Set("X-CCU-Addon-Request", "1")
	if handle != "" {
		r.Header.Set(httpapi.HandleHeader, handle)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b, res.Header
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
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
	// /ro is a read-only mount and /full a file system of 256 KiB, for the file API
	run(t, "docker", "run", "-d", "--rm", "--name", name, "-p", "127.0.0.1:0:22", "--tmpfs", "/ro:ro", "--tmpfs", "/full:size=256k", image)
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

	t.Run("root's files over OpenSSH's SFTP server: a save keeps mode and owner, backs up, and knows the mounts", func(t *testing.T) {
		sh := func(cmd string) string { return run(t, "docker", "exec", name, "sh", "-c", cmd) }
		code, out := s.req("POST", "api/v1/sessions", map[string]any{"kind": "files", "password": password})
		if code != 201 {
			t.Fatalf("sign-in: %d %v", code, out)
		}
		id, handle := out["session"].(map[string]any)["id"].(string), out["handle"].(string)
		defer s.mgr.End(id, sessions.ReasonEnded)
		q := func(p string) string { return "path=" + url.QueryEscape(p) }
		js := func(method, call string, body any) (int, map[string]any) {
			t.Helper()
			var b []byte
			hdr := map[string]string{}
			if body != nil {
				b, _ = json.Marshal(body)
				hdr["Content-Type"] = "application/json"
			}
			st, raw, _ := s.fs(method, id, handle, call, b, hdr)
			var out map[string]any
			_ = json.Unmarshal(raw, &out)
			return st, out
		}
		read := func(p string) (int, []byte, map[string]any) {
			t.Helper()
			st, body, h := s.fs("GET", id, handle, "read?"+q(p), nil, nil)
			var meta map[string]any
			_ = json.Unmarshal([]byte(h.Get(httpapi.MetaHeader)), &meta)
			return st, body, meta
		}
		save := func(p, content, replaces string) (int, map[string]any) {
			t.Helper()
			st, raw, _ := s.fs("PUT", id, handle, "write?"+q(p), []byte(content), map[string]string{"If-Match": `"` + replaces + `"`})
			var out map[string]any
			_ = json.Unmarshal(raw, &out)
			return st, out
		}

		// the root directory as sshd's own SFTP server lists it, with the names of the accounts
		st, list := js("GET", "list?"+q("/"), nil)
		if st != 200 || list["writable"] != true {
			t.Fatalf("list /: %d %v", st, list)
		}
		names := map[string]map[string]any{}
		for _, e := range list["entries"].([]any) {
			names[e.(map[string]any)["name"].(string)] = e.(map[string]any)
		}
		if names["etc"]["type"] != "dir" || names["etc"]["owner"] != "root" || names["proc"]["locked"] != "kernel" || names["ro"]["locked"] != "read-only" || names["full"]["locked"] != nil {
			t.Fatalf("the root directory: etc %v, proc %v, ro %v, full %v", names["etc"], names["proc"], names["ro"], names["full"])
		}

		// a file of another account, closed to others
		sh("mkdir -p /usr/local/etc/config && printf 'Listen Port = 32001\\n' > /usr/local/etc/config/rfd.conf && chown nobody:nobody /usr/local/etc/config/rfd.conf && chmod 0640 /usr/local/etc/config/rfd.conf && ln -s /usr/local/etc/config /etc/config")
		conf := "/usr/local/etc/config/rfd.conf"
		st, body, meta := read("/etc/config/rfd.conf")
		if st != 200 || string(body) != "Listen Port = 32001\n" || meta["real"] != conf || meta["owner"] != "nobody" || meta["mode"] != "0640" || meta["editable"] != true || meta["sha256"] != sha(body) {
			t.Fatalf("read: %d %q %v", st, body, meta)
		}
		st, saved := save(conf, "Listen Port = 32002\n", sha(body))
		if st != 200 || saved["in_place"] != false {
			t.Fatalf("save: %d %v", st, saved)
		}
		if got := sh("stat -c '%U:%G %a' " + conf + "; cat " + conf); got != "nobody:nobody 640\nListen Port = 32002" {
			t.Fatalf("after the save: %q", got)
		}
		// the backup is root's and closed, in closed directories, with the mark that keeps it out of a system backup
		backup := saved["backup"].(string)
		if got := sh("stat -c '%U %a' " + backup + " /usr/local/shell-backups /usr/local/shell-backups/usr/local/etc/config /usr/local/shell-backups/.nobackup; cat " + backup); got != "root 600\nroot 700\nroot 700\nroot 600\nListen Port = 32001" {
			t.Fatalf("the backup: %q", got)
		}
		if left := sh("ls -a /usr/local/etc/config | grep -c shell- || true"); left != "0" {
			t.Fatalf("a temporary file stayed beside the file")
		}
		// changed by someone else in between: a conflict, and nothing is written
		sh("echo theirs > " + conf)
		if st, out := save(conf, "mine\n", saved["sha256"].(string)); st != 409 || out["error"] != "conflict" || out["sha256"] != sha([]byte("theirs\n")) {
			t.Fatalf("conflict: %d %v", st, out)
		}
		if got := sh("cat " + conf); got != "theirs" {
			t.Fatalf("a conflict wrote: %q", got)
		}

		// /etc/hosts is a bind mount in a container, as the writable passwd is on a system: no rename over it
		st, body, meta = read("/etc/hosts")
		if st != 200 || meta["editable"] != true {
			t.Fatalf("read /etc/hosts: %d %v", st, meta)
		}
		st, saved = save("/etc/hosts", string(body)+"127.0.0.2 in-place\n", sha(body))
		if st != 200 || saved["in_place"] != true {
			t.Fatalf("save /etc/hosts: %d %v", st, saved)
		}
		if got := sh("tail -n 1 /etc/hosts; ls -a /etc | grep -c shell- || true"); got != "127.0.0.2 in-place\n0" {
			t.Fatalf("after the save in place: %q", got)
		}

		// the kernel's files: read to the end though they state no size, never editable; a device is not opened
		if st, body, meta = read("/proc/version"); st != 200 || !strings.HasPrefix(string(body), "Linux version") || meta["editable"] != false || meta["why"] != "kernel" {
			t.Fatalf("/proc/version: %d %q %v", st, body, meta)
		}
		if st, _, _ := read("/dev/zero"); st != 422 {
			t.Fatalf("/dev/zero: %d", st)
		}
		// a read-only mount is known before the system says no
		if st, out := js("POST", "create", map[string]any{"path": "/ro/new"}); st != 403 || out["error"] != "read-only" {
			t.Fatalf("create on a read-only mount: %d %v", st, out)
		}
		if st, out := js("GET", "list?"+q("/ro"), nil); st != 200 || out["writable"] != false || out["reason"] != "read-only" || out["mount"] != "/ro" {
			t.Fatalf("list /ro: %d %v", st, out)
		}

		// upload and download: three megabytes, streamed both ways, the same bytes
		big := bytes.Repeat([]byte("0123456789abcdef\x00\xff"), 3<<20/18)
		if st, raw, _ := s.fs("POST", id, handle, "upload?"+q("/usr/local/up.bin"), big, nil); st != 201 {
			t.Fatalf("upload: %d %s", st, raw)
		}
		if got := sh("sha256sum /usr/local/up.bin | cut -d' ' -f1; stat -c '%U %a' /usr/local/up.bin"); got != sha(big)+"\nroot 644" {
			t.Fatalf("the upload on the system: %q", got)
		}
		st, tk := js("POST", "download", map[string]any{"path": "/usr/local/up.bin"})
		if st != 201 {
			t.Fatalf("ticket: %d %v", st, tk)
		}
		st, got, h := s.fs("GET", id, "", "download/"+tk["ticket"].(string), nil, nil)
		if st != 200 || sha(got) != sha(big) || !strings.Contains(h.Get("Content-Disposition"), "up.bin") {
			t.Fatalf("download: %d, %d bytes", st, len(got))
		}
		// a full file system: the upload is refused with the reason and leaves nothing; a save there leaves the file alone
		if st, raw, _ := s.fs("POST", id, handle, "upload?"+q("/full/big.bin"), big, nil); st != 507 || !strings.Contains(string(raw), "no-space") {
			t.Fatalf("upload to a full file system: %d %s", st, raw)
		}
		if got := sh("ls -a /full | grep -vc '^\\.\\.\\?$' || true"); got != "0" {
			t.Fatalf("a part of the upload stayed: %s", sh("ls -la /full"))
		}
		sh("printf 'small\\n' > /full/f.conf; dd if=/dev/zero of=/full/fill bs=1k count=300 2>/dev/null || true")
		// (the backup goes to /usr/local, which has room: it is the write beside the file that fails)
		if st, out := save("/full/f.conf", strings.Repeat("larger than what is left\n", 2000), sha([]byte("small\n"))); st != 507 || out["error"] != "no-space" {
			t.Fatalf("save on a full file system: %d %v", st, out)
		}
		if got := sh("cat /full/f.conf; ls -a /full | grep -c shell- || true"); got != "small\n0" {
			t.Fatalf("after the refused save: %q", got)
		}

		// mode and owner, rename, a folder with everything in it
		if st, out := js("POST", "chmod", map[string]any{"path": "/usr/local/up.bin", "mode": "4750"}); st != 200 {
			t.Fatalf("chmod: %d %v", st, out)
		}
		if st, out := js("POST", "chown", map[string]any{"path": "/usr/local/up.bin", "uid": 65534, "gid": 0}); st != 200 {
			t.Fatalf("chown: %d %v", st, out)
		}
		// (the kernel clears the set-id bit at a change of owner: the page sets the owner first for that reason)
		if got := sh("stat -c '%U:%G %a' /usr/local/up.bin"); got != "nobody:root 750" {
			t.Fatalf("after chmod and chown: %q", got)
		}
		if st, out := js("POST", "mkdir", map[string]any{"path": "/usr/local/dir"}); st != 201 {
			t.Fatalf("mkdir: %d %v", st, out)
		}
		if st, out := js("POST", "rename", map[string]any{"from": "/usr/local/up.bin", "to": "/usr/local/dir/moved.bin"}); st != 200 {
			t.Fatalf("rename: %d %v", st, out)
		}
		if st, out := js("POST", "rename", map[string]any{"from": conf, "to": "/usr/local/dir/moved.bin"}); st != 409 || out["error"] != "exists" {
			t.Fatalf("rename over a file: %d %v", st, out)
		}
		sh("ln -s /etc /usr/local/dir/to-etc && mkdir -p /usr/local/dir/a/b && touch /usr/local/dir/a/b/c")
		if st, out := js("POST", "remove", map[string]any{"path": "/usr/local/dir"}); st != 409 || out["error"] != "not-empty" || out["entries"] != float64(3) {
			t.Fatalf("remove a folder with something in it: %d %v", st, out)
		}
		if st, out := js("POST", "remove", map[string]any{"path": "/usr/local/dir", "recursive": true}); st != 200 || out["removed"] != float64(6) {
			t.Fatalf("remove with everything: %d %v", st, out)
		}
		if got := sh("test -e /usr/local/dir && echo there; test -f /etc/passwd && echo passwd"); got != "passwd" {
			t.Fatalf("after the delete: %q", got)
		}
		// restore the first version; the present one is kept
		_, bl := js("GET", "backups?"+q(conf), nil)
		list2 := bl["backups"].([]any)
		oldest := list2[len(list2)-1].(map[string]any)["name"].(string)
		if st, out := js("POST", "restore", map[string]any{"path": conf, "name": oldest}); st != 200 {
			t.Fatalf("restore: %d %v", st, out)
		}
		if got := sh("cat " + conf + "; stat -c '%U:%G %a' " + conf); got != "Listen Port = 32001\nnobody:nobody 640" {
			t.Fatalf("after the restore: %q", got)
		}
		lines, _ := json.Marshal(s.audit.List())
		for _, want := range []string{"session-open kind=files user=admin", `file-write id=` + id + ` user=admin path=\"` + conf + `\" bytes=20`, `path=\"/etc/hosts\"`, "in_place=true", "file-upload id=" + id, "file-download id=" + id, "file-restore id=" + id} {
			if !strings.Contains(string(lines), want) {
				t.Errorf("audit lacks %q", want)
			}
		}
		if strings.Contains(string(lines), "32002") || strings.Contains(string(lines), "theirs") {
			t.Errorf("the audit holds what a file holds")
		}
	})

	t.Run("a key for root as the system writes it: only from its address, only until its time, no forwarding", func(t *testing.T) {
		// What the key mode stands on: openccu-lite writes an addon's key into root's authorized keys
		// as restrict,pty,from="127.0.0.1,::1",expiry-time="<a minute ahead>Z" each time an
		// administrator arms it. sshd is the one that holds those limits; this is what it does.
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		signer, err := ssh.NewSignerFromKey(priv)
		if err != nil {
			t.Fatal(err)
		}
		pub := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
		// the address sshd sees this test come from (the bridge's gateway): "the system itself" here
		from := run(t, "docker", "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.Gateway}}{{end}}", name)
		stamp := func(d time.Duration) string { return time.Now().UTC().Add(d).Format("20060102150405") + "Z" }
		arm := func(from, expiry string) {
			t.Helper()
			line := fmt.Sprintf(`restrict,pty,from="%s",expiry-time="%s" %s openccu-lite-addon:shell`, from, expiry, pub)
			run(t, "docker", "exec", name, "sh", "-c", "mkdir -p /root/.ssh && chmod 700 /root/.ssh && cat > /root/.ssh/authorized_keys <<'EOF'\n"+line+"\nEOF\nchmod 600 /root/.ssh/authorized_keys")
		}
		defer run(t, "docker", "exec", name, "rm", "-f", "/root/.ssh/authorized_keys")

		// not armed: there is no line
		if _, err := s.ssh.OpenWithKey(ctx, signer, sshc.Size{}); !errors.Is(err, sshc.ErrKeyRefused) {
			t.Fatalf("a key without a line: %v", err)
		}
		// armed for this address, a minute ahead: a root shell on a pty ("restrict" alone would give none)
		arm(from, stamp(time.Minute))
		sh, err := s.ssh.OpenWithKey(ctx, signer, sshc.Size{Cols: 100, Rows: 30})
		if err != nil {
			t.Fatalf("the armed key: %v", err)
		}
		var out bytes.Buffer
		got := make(chan struct{})
		go func() {
			defer close(got)
			buf := make([]byte, 4096)
			for !strings.Contains(out.String(), "KEY-0-/dev/pts/") {
				n, err := sh.Read(buf)
				out.Write(buf[:n])
				if err != nil {
					return
				}
			}
		}()
		_, _ = sh.Write([]byte("echo KEY-$(id -u)-$(tty)\r"))
		select {
		case <-got:
		case <-time.After(15 * time.Second):
		}
		sh.Close()
		<-got
		if !strings.Contains(out.String(), "KEY-0-/dev/pts/") {
			t.Fatalf("no root shell on a pty with the key: %q", tail(out.String()))
		}
		// the files work under the same line
		fl, err := s.ssh.OpenFilesWithKey(ctx, signer)
		if err != nil {
			t.Fatalf("SFTP with the armed key: %v", err)
		}
		if _, err := fl.SFTP.Stat("/etc/passwd"); err != nil {
			t.Errorf("SFTP under restrict,pty: %v", err)
		}
		fl.Close()
		// no forwarding through it: the key opens a shell and files on the system, not a way into its network
		hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(run(t, "docker", "exec", name, "cat", "/etc/ssh/ssh_host_ed25519_key.pub")))
		if err != nil {
			t.Fatal(err)
		}
		direct, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{User: "root", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
			HostKeyCallback: ssh.FixedHostKey(hostKey), HostKeyAlgorithms: []string{ssh.KeyAlgoED25519}, Timeout: 10 * time.Second})
		if err != nil {
			t.Fatalf("a plain connection with the armed key: %v", err)
		}
		if c, err := direct.Dial("tcp", "127.0.0.1:22"); err == nil {
			c.Close()
			t.Errorf("a forwarded connection was opened through the restricted key")
		}
		if l, err := direct.Listen("tcp", "127.0.0.1:0"); err == nil {
			l.Close()
			t.Errorf("a remote forward was granted to the restricted key")
		}
		direct.Close()
		// its time is up: refused, though the line is still there
		arm(from, stamp(-time.Minute))
		if _, err := s.ssh.OpenWithKey(ctx, signer, sshc.Size{}); !errors.Is(err, sshc.ErrKeyRefused) {
			t.Fatalf("after the expiry: %v", err)
		}
		// for the loopback only, as the system writes it: this test comes from another address
		arm("127.0.0.1,::1", stamp(time.Minute))
		if _, err := s.ssh.OpenWithKey(ctx, signer, sshc.Size{}); !errors.Is(err, sshc.ErrKeyRefused) {
			t.Fatalf("from another address than the line names: %v", err)
		}
		// a server that takes keys only takes this key: the key mode is what such a system gets
		arm(from, stamp(time.Minute))
		run(t, "docker", "exec", name, "sh", "-c", "echo 'PasswordAuthentication no' > /etc/ssh/sshd_config.d/keys.conf && kill -HUP 1")
		defer func() {
			run(t, "docker", "exec", name, "sh", "-c", "rm -f /etc/ssh/sshd_config.d/keys.conf && kill -HUP 1")
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
		sh, err = s.ssh.OpenWithKey(ctx, signer, sshc.Size{})
		if err != nil {
			t.Fatalf("the armed key on a server that takes keys only: %v", err)
		}
		sh.Close()
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
		time.Sleep(2100 * time.Millisecond) // the state's probe is kept for two seconds
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
