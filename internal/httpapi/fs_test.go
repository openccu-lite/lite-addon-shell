package httpapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/openccu-lite/lite-addon-shell/internal/httpapi"
)

// The file API against the fake sshd's SFTP over a scratch directory seeded like a small system.

const conf = "/usr/local/etc/config/rfd.conf"

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func (e *env) openFiles(sid string) (id, handle string) {
	e.t.Helper()
	st, out, _ := e.req("POST", "api/v1/sessions", sid, map[string]any{"kind": "files", "password": "root-pass"}, nil)
	if st != 201 {
		e.t.Fatalf("open files: %d %v", st, out)
	}
	sess := out["session"].(map[string]any)
	if sess["kind"] != "files" {
		e.t.Fatalf("kind %v", sess["kind"])
	}
	return sess["id"].(string), out["handle"].(string)
}

// raw is a request for files as the gate hands it on, with the handle in its header; body is sent
// as it is.
func (e *env) raw(method, path, sid, handle string, body []byte, hdr map[string]string) (int, []byte, http.Header) {
	e.t.Helper()
	r, _ := http.NewRequest(method, e.srv.URL+"/addons/shell/"+path, bytes.NewReader(body))
	r.Header.Set("X-Forwarded-For", "192.0.2.10")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Occulite-Session", sid)
	r.Header.Set("X-Occulite-Auth", "session")
	if handle != "" {
		r.Header.Set(httpapi.HandleHeader, handle)
	}
	if method != http.MethodGet {
		r.Header.Set("X-CCU-Addon-Request", "1")
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b, res.Header
}

// fs is a JSON request for files; it answers the status and the decoded answer.
func (e *env) fs(method, id, handle, call string, body any) (int, map[string]any) {
	e.t.Helper()
	var b []byte
	hdr := map[string]string{}
	if body != nil {
		b, _ = json.Marshal(body)
		hdr["Content-Type"] = "application/json"
	}
	st, raw, _ := e.raw(method, "api/v1/sessions/"+id+"/fs/"+call, sidAdmin, handle, b, hdr)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return st, out
}

func (e *env) file(p string) string {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.root, filepath.FromSlash(p)))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func (e *env) audited(want string) bool {
	for _, l := range e.audit.List() {
		if strings.Contains(l.Line, want) {
			return true
		}
	}
	return false
}

func q(p string) string { return url.QueryEscape(p) }

// A files session is a session like a terminal: the same sign-in, the same limit, its own socket;
// its files answer only to the handle in the header, from the web session that opened it.
func TestFilesSessionAnswersOnlyToItsHandleAndItsWebSession(t *testing.T) {
	e := newEnv(t)
	e.enable()
	id, handle := e.openFiles(sidAdmin)
	if !e.audited("session-open kind=files user=admin client=192.0.2.10 id=" + id) {
		t.Fatalf("no audit line for the files session: %v", e.audit.List())
	}
	st, out := e.fs("GET", id, handle, "list?path=/usr/local/etc/config", nil)
	if st != 200 || out["writable"] != true || out["real"] != "/usr/local/etc/config" || len(out["entries"].([]any)) < 8 || out["files_read_only"] != false {
		t.Fatalf("list: %d %v", st, out)
	}
	// no handle, a wrong one, the handle in the address: one answer
	for _, c := range []struct{ call, handle string }{
		{"list?path=/", ""}, {"list?path=/", strings.Repeat("0", 64)}, {"list?path=/&handle=" + handle, ""},
	} {
		if st, out := e.fs("GET", id, c.handle, c.call, nil); st != 403 || out["error"] != "no-session" {
			t.Errorf("%+v: %d %v", c, st, out)
		}
	}
	// another administrator who learned the handle
	if st, raw, _ := e.raw("GET", "api/v1/sessions/"+id+"/fs/list?path=/", sidOther, handle, nil, nil); st != 403 || !strings.Contains(string(raw), "no-session") {
		t.Errorf("another web session: %d %s", st, raw)
	}
	// a lower account does not get to the API at all
	if st, _, _ := e.raw("GET", "api/v1/sessions/"+id+"/fs/list?path=/", sidUser, handle, nil, nil); st != 403 {
		t.Errorf("a configure account: %d", st)
	}
	// over plain HTTP nothing is read
	if st, raw, _ := e.raw("GET", "api/v1/sessions/"+id+"/fs/list?path=/", sidAdmin, handle, nil, map[string]string{"X-Forwarded-Proto": "http"}); st != 403 || !strings.Contains(string(raw), "insecure-transport") {
		t.Errorf("over HTTP: %d %s", st, raw)
	}
	// through a tunnel addon neither, unless allowed
	if st, raw, _ := e.raw("GET", "api/v1/sessions/"+id+"/fs/list?path=/", sidAdmin, handle, nil, map[string]string{"X-Occulite-Ingress": "wireguard"}); st != 403 || !strings.Contains(string(raw), "tunnel-refused") {
		t.Errorf("through a tunnel: %d %s", st, raw)
	}
	// a terminal's handle opens no file, and the terminal's socket is not the files session's
	tid, thandle := e.open(sidAdmin)
	if st, out := e.fs("GET", tid, thandle, "list?path=/", nil); st != 403 || out["error"] != "no-session" {
		t.Errorf("a terminal as a files session: %d %v", st, out)
	}
	if _, res, err := e.dial(id, sidAdmin, nil); err == nil || res == nil || res.StatusCode != 404 {
		t.Errorf("the terminal's socket on a files session: %v %v", res, err)
	}
	// both kinds count against the one limit
	if st, out, _ := e.req("POST", "api/v1/sessions", sidAdmin, map[string]any{"kind": "files", "password": "root-pass"}, nil); st != 409 || out["error"] != "too-many-sessions" {
		t.Errorf("a third session: %d %v", st, out)
	}
	if st, out, _ := e.req("POST", "api/v1/sessions", sidAdmin, map[string]any{"kind": "tape", "password": "root-pass"}, nil); st != 422 {
		t.Errorf("an unknown kind: %d %v", st, out)
	}
	// a request is the user's doing: the idle time starts again
	before, _ := e.mgr.Get(id)
	time.Sleep(20 * time.Millisecond)
	e.fs("GET", id, handle, "stat?path=/", nil)
	if after, _ := e.mgr.Get(id); after.Input <= before.Input {
		t.Errorf("a request did not reset the idle time: %d then %d", before.Input, after.Input)
	}
	// the files session's socket tells the tab the end and its reason at once
	ws, err := e.link(id, sidAdmin)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = ws.Write(ctx, websocket.MessageText, []byte(`{"handle":"`+handle+`"}`))
	if _, data, err := ws.Read(ctx); err != nil || !strings.Contains(string(data), `"t":"ready"`) || !strings.Contains(string(data), `"kind":"files"`) {
		t.Fatalf("the link's first message: %s %v", data, err)
	}
	if st, _, _ := e.req("DELETE", "api/v1/sessions/"+id, sidOther, nil, nil); st != 200 {
		t.Fatalf("end: %d", st)
	}
	if _, data, err := ws.Read(ctx); err != nil || !strings.Contains(string(data), `"reason":"ended"`) {
		t.Fatalf("the link's end: %s %v", data, err)
	}
	if st, out := e.fs("GET", id, handle, "list?path=/", nil); st != 403 || out["error"] != "no-session" {
		t.Errorf("after the end: %d %v", st, out)
	}
}

func (e *env) link(id, sid string) (*websocket.Conn, error) {
	h := http.Header{}
	h.Set("X-Forwarded-For", "192.0.2.10")
	h.Set("X-Forwarded-Proto", "https")
	h.Set("X-Occulite-Session", sid)
	h.Set("X-Occulite-Auth", "session")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/addons/shell/api/v1/sessions/"+id+"/link", &websocket.DialOptions{HTTPHeader: h})
	return ws, err
}

// Reading, saving with the conflict check, and what the audit lines hold: paths, never content.
func TestFilesReadSaveAndTheirAuditLines(t *testing.T) {
	e := newEnv(t)
	e.enable()
	id, handle := e.openFiles(sidAdmin)
	base := "api/v1/sessions/" + id + "/fs/"
	old := e.file(conf)

	st, body, h := e.raw("GET", base+"read?path="+q(conf), sidAdmin, handle, nil, nil)
	var meta map[string]any
	if err := json.Unmarshal([]byte(h.Get(httpapi.MetaHeader)), &meta); err != nil {
		t.Fatalf("the file's description: %q (%v)", h.Get(httpapi.MetaHeader), err)
	}
	if st != 200 || string(body) != old || meta["kind"] != "text" || meta["editable"] != true || meta["sha256"] != sha(old) || meta["real"] != conf || meta["mode"] != "0644" || meta["owner"] != "root" {
		t.Fatalf("read: %d %q %v", st, body, meta)
	}
	// a name outside ASCII travels in the header as escapes
	if st, _, _ := e.raw("POST", base+"upload?path="+q("/usr/local/grüße 😀.txt"), sidAdmin, handle, []byte("hallo\n"), nil); st != 201 {
		t.Fatalf("upload: %d", st)
	}
	_, _, h = e.raw("GET", base+"read?path="+q("/usr/local/grüße 😀.txt"), sidAdmin, handle, nil, nil)
	for _, c := range []byte(h.Get(httpapi.MetaHeader)) {
		if c >= 0x80 {
			t.Fatalf("the header holds a byte outside ASCII: %q", h.Get(httpapi.MetaHeader))
		}
	}
	if err := json.Unmarshal([]byte(h.Get(httpapi.MetaHeader)), &meta); err != nil || meta["name"] != "grüße 😀.txt" {
		t.Fatalf("the name: %v %v", meta["name"], err)
	}

	// a save names what it replaces
	secret := "Password = hunter2\n"
	st, body, _ = e.raw("PUT", base+"write?path="+q(conf), sidAdmin, handle, []byte(secret), nil)
	if st != 428 {
		t.Fatalf("a save without If-Match: %d %s", st, body)
	}
	st, body, _ = e.raw("PUT", base+"write?path="+q(conf), sidAdmin, handle, []byte(secret), map[string]string{"If-Match": `"` + sha(old) + `"`})
	var saved map[string]any
	_ = json.Unmarshal(body, &saved)
	if st != 200 || e.file(conf) != secret || saved["sha256"] != sha(secret) || saved["in_place"] != false || !strings.HasPrefix(saved["backup"].(string), "/usr/local/shell-backups/usr/local/etc/config/rfd.conf.") {
		t.Fatalf("save: %d %s", st, body)
	}
	if e.file(saved["backup"].(string)) != old {
		t.Fatal("the backup is not the old content")
	}
	if !e.audited(`file-write id=` + id + ` user=admin path="` + conf + `" bytes=19 backup="` + saved["backup"].(string) + `" in_place=false`) {
		t.Fatalf("no audit line for the save: %v", e.audit.List())
	}
	// the file changed behind the editor: a conflict, with the file's state, and nothing written
	if err := os.WriteFile(filepath.Join(e.root, conf), []byte("someone else\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, body, _ = e.raw("PUT", base+"write?path="+q(conf), sidAdmin, handle, []byte("mine\n"), map[string]string{"If-Match": sha(secret)})
	var conflict map[string]any
	_ = json.Unmarshal(body, &conflict)
	if st != 409 || conflict["error"] != "conflict" || conflict["sha256"] != sha("someone else\n") || e.file(conf) != "someone else\n" {
		t.Fatalf("conflict: %d %s", st, body)
	}
	// over 2 MB the editor does not save
	if st, _, _ := e.raw("PUT", base+"write?path="+q(conf), sidAdmin, handle, bytes.Repeat([]byte("a"), 2<<20+1), map[string]string{"If-Match": sha("someone else\n")}); st != 413 {
		t.Fatalf("a save over 2 MB: %d", st)
	}
	// on the read-only root file system
	if st, body, _ := e.raw("PUT", base+"write?path=/etc/hostname", sidAdmin, handle, []byte("x\n"), map[string]string{"If-Match": sha("openccu-lite\n")}); st != 403 || !strings.Contains(string(body), `"read-only"`) {
		t.Fatalf("a save on the root file system: %d %s", st, body)
	}
	// the backups and a restore
	st, out := e.fs("GET", id, handle, "backups?path="+q(conf), nil)
	list := out["backups"].([]any)
	if st != 200 || len(list) != 1 || out["keep"] != float64(10) || out["root"] != "/usr/local/shell-backups" {
		t.Fatalf("backups: %d %v", st, out)
	}
	name := list[0].(map[string]any)["name"].(string)
	st, out = e.fs("POST", id, handle, "restore", map[string]any{"path": conf, "name": name})
	if st != 200 || e.file(conf) != old || e.file(out["backup"].(string)) != "someone else\n" {
		t.Fatalf("restore: %d %v", st, out)
	}
	if !e.audited(`file-restore id=` + id + ` user=admin path="` + conf + `" from="` + name + `"`) {
		t.Fatalf("no audit line for the restore: %v", e.audit.List())
	}
	if st, out = e.fs("POST", id, handle, "restore", map[string]any{"path": conf, "name": "../../../../../../etc/passwd"}); st != 422 {
		t.Fatalf("a restore from elsewhere: %d %v", st, out)
	}
	// no line holds what a file holds
	for _, l := range e.audit.List() {
		if strings.Contains(l.Line, "hunter2") || strings.Contains(l.Line, "someone else") || strings.Contains(l.Line, "Listen Port") {
			t.Fatalf("an audit line holds a file's content: %s", l.Line)
		}
	}
}

// The other changes, each with its audit line, and the setting that makes the page read-only.
func TestFilesChangesAndTheReadOnlySetting(t *testing.T) {
	e := newEnv(t)
	e.enable()
	id, handle := e.openFiles(sidAdmin)
	base := "api/v1/sessions/" + id + "/fs/"
	dir := "/usr/local/etc/config"

	if st, out := e.fs("POST", id, handle, "create", map[string]any{"path": dir + "/new.conf"}); st != 201 || out["created"] != true {
		t.Fatalf("create: %d %v", st, out)
	}
	if st, out := e.fs("POST", id, handle, "create", map[string]any{"path": dir + "/new.conf"}); st != 409 || out["error"] != "exists" {
		t.Fatalf("create again: %d %v", st, out)
	}
	if st, out := e.fs("POST", id, handle, "mkdir", map[string]any{"path": dir + "/sub"}); st != 201 {
		t.Fatalf("mkdir: %d %v", st, out)
	}
	if st, out := e.fs("POST", id, handle, "rename", map[string]any{"from": dir + "/new.conf", "to": dir + "/sub/moved.conf"}); st != 200 {
		t.Fatalf("rename: %d %v", st, out)
	}
	if st, out := e.fs("POST", id, handle, "rename", map[string]any{"from": dir + "/crontab", "to": conf}); st != 409 || out["error"] != "exists" {
		t.Fatalf("rename over a file: %d %v", st, out)
	}
	if st, out := e.fs("POST", id, handle, "chmod", map[string]any{"path": dir + "/sub/moved.conf", "mode": "0600"}); st != 200 {
		t.Fatalf("chmod: %d %v", st, out)
	}
	for _, mode := range []string{"rw-", "99", "12345", ""} {
		if st, _ := e.fs("POST", id, handle, "chmod", map[string]any{"path": dir + "/sub/moved.conf", "mode": mode}); st != 422 {
			t.Errorf("chmod %q: %d", mode, st)
		}
	}
	if st, out := e.fs("POST", id, handle, "chown", map[string]any{"path": dir + "/sub/moved.conf", "uid": 33, "gid": 33}); st != 200 {
		t.Fatalf("chown: %d %v", st, out)
	}
	if st, _ := e.fs("POST", id, handle, "chown", map[string]any{"path": dir + "/sub/moved.conf", "uid": 33}); st != 422 {
		t.Errorf("chown without a group: %d", st)
	}
	st, out := e.fs("GET", id, handle, "stat?path="+q(dir+"/sub/moved.conf"), nil)
	if st != 200 || out["mode"] != "0600" || out["owner"] != "www-data" || out["writable"] != true {
		t.Fatalf("stat: %d %v", st, out)
	}
	if st, out = e.fs("GET", id, handle, "ids", nil); st != 200 || len(out["users"].([]any)) != 5 {
		t.Fatalf("ids: %d %v", st, out)
	}
	if st, out = e.fs("POST", id, handle, "remove", map[string]any{"path": dir + "/sub"}); st != 409 || out["error"] != "not-empty" || out["entries"] != float64(1) {
		t.Fatalf("remove a folder with something in it: %d %v", st, out)
	}
	if st, out = e.fs("POST", id, handle, "remove", map[string]any{"path": dir + "/sub", "recursive": true}); st != 200 || out["removed"] != float64(2) {
		t.Fatalf("remove with everything: %d %v", st, out)
	}
	for _, want := range []string{
		`file-create id=` + id + ` user=admin path="` + dir + `/new.conf"`,
		`file-create id=` + id + ` user=admin path="` + dir + `/sub" dir=true`,
		`file-rename id=` + id + ` user=admin from="` + dir + `/new.conf" to="` + dir + `/sub/moved.conf"`,
		`file-chmod id=` + id + ` user=admin path="` + dir + `/sub/moved.conf" mode=0600`,
		`file-chown id=` + id + ` user=admin path="` + dir + `/sub/moved.conf" uid=33 gid=33`,
		`file-delete id=` + id + ` user=admin path="` + dir + `/sub" entries=2 complete=true`,
	} {
		if !e.audited(want) {
			t.Errorf("no audit line %s in %v", want, e.audit.List())
		}
	}

	// upload: streamed, asks before it replaces, bounded
	if st, body, _ := e.raw("POST", base+"upload?path="+q(dir+"/up.bin"), sidAdmin, handle, []byte{0, 1, 2}, nil); st != 201 {
		t.Fatalf("upload: %d %s", st, body)
	}
	if st, body, _ := e.raw("POST", base+"upload?path="+q(dir+"/up.bin"), sidAdmin, handle, []byte("x"), nil); st != 409 || !strings.Contains(string(body), `"exists"`) {
		t.Fatalf("upload over a file: %d %s", st, body)
	}
	st, body, _ := e.raw("POST", base+"upload?path="+q(dir+"/up.bin")+"&overwrite=1", sidAdmin, handle, []byte("new"), nil)
	if st != 201 || e.file(dir+"/up.bin") != "new" || !strings.Contains(string(body), `"backup":"/usr/local/shell-backups/`) {
		t.Fatalf("upload with overwrite: %d %s", st, body)
	}
	if !e.audited(`file-upload id=` + id + ` user=admin path="` + dir + `/up.bin" bytes=3 backup="/usr/local/shell-backups/`) {
		t.Errorf("no audit line for the upload: %v", e.audit.List())
	}

	// download: a ticket, good once, for this web session only; the handle is in no address
	st, out = e.fs("POST", id, handle, "download", map[string]any{"path": dir + "/up.bin"})
	if st != 201 || out["name"] != "up.bin" || out["size"] != float64(3) {
		t.Fatalf("download ticket: %d %v", st, out)
	}
	ticket := out["ticket"].(string)
	if strings.Contains(ticket, handle) || len(ticket) < 32 {
		t.Fatalf("the ticket: %q", ticket)
	}
	if st, _, _ := e.raw("GET", base+"download/"+ticket, sidOther, "", nil, nil); st != 403 {
		t.Fatalf("another web session with the ticket: %d", st)
	}
	// ... and that try spent it
	if st, _, _ := e.raw("GET", base+"download/"+ticket, sidAdmin, "", nil, nil); st != 403 {
		t.Fatalf("a spent ticket: %d", st)
	}
	_, out = e.fs("POST", id, handle, "download", map[string]any{"path": dir + "/up.bin"})
	st, body, h := e.raw("GET", base+"download/"+out["ticket"].(string), sidAdmin, "", nil, nil)
	if st != 200 || string(body) != "new" || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") || !strings.Contains(h.Get("Content-Disposition"), "up.bin") || h.Get("Content-Length") != "3" {
		t.Fatalf("download: %d %q %v", st, body, h)
	}
	if !e.audited(`file-download id=` + id + ` user=admin path="` + dir + `/up.bin" bytes=3`) {
		t.Errorf("no audit line for the download: %v", e.audit.List())
	}
	if st, out = e.fs("POST", id, handle, "download", map[string]any{"path": dir}); st != 422 {
		t.Fatalf("a ticket for a folder: %d %v", st, out)
	}

	// read-only altogether: everything is shown and downloaded, nothing is changed
	set := e.store.Settings()
	set.FilesReadOnly = true
	if st, out, _ := e.req("PUT", "api/v1/settings", sidAdmin, set, nil); st != 200 || out["settings"].(map[string]any)["files_read_only"] != true {
		t.Fatalf("the setting: %d %v", st, out)
	}
	if !e.audited("files_read_only=true") {
		t.Errorf("the setting's audit line: %v", e.audit.List())
	}
	for _, c := range []struct {
		method, call string
		body         any
	}{
		{"POST", "create", map[string]any{"path": dir + "/x"}}, {"POST", "mkdir", map[string]any{"path": dir + "/x"}},
		{"POST", "rename", map[string]any{"from": conf, "to": dir + "/x"}}, {"POST", "remove", map[string]any{"path": conf}},
		{"POST", "chmod", map[string]any{"path": conf, "mode": "0600"}}, {"POST", "chown", map[string]any{"path": conf, "uid": 0, "gid": 0}},
		{"POST", "restore", map[string]any{"path": conf, "name": "x"}},
	} {
		if st, out := e.fs(c.method, id, handle, c.call, c.body); st != 403 || out["error"] != "read-only-setting" {
			t.Errorf("%s with the page read-only: %d %v", c.call, st, out)
		}
	}
	if st, body, _ := e.raw("PUT", base+"write?path="+q(conf), sidAdmin, handle, []byte("x"), map[string]string{"If-Match": sha(e.file(conf))}); st != 403 || !strings.Contains(string(body), "read-only-setting") {
		t.Errorf("a save with the page read-only: %d %s", st, body)
	}
	if st, body, _ := e.raw("POST", base+"upload?path="+q(dir+"/y"), sidAdmin, handle, []byte("x"), nil); st != 403 || !strings.Contains(string(body), "read-only-setting") {
		t.Errorf("an upload with the page read-only: %d %s", st, body)
	}
	_, _, h = e.raw("GET", base+"read?path="+q(conf), sidAdmin, handle, nil, nil)
	var meta map[string]any
	_ = json.Unmarshal([]byte(h.Get(httpapi.MetaHeader)), &meta)
	if meta["editable"] != false || meta["why"] != "setting" {
		t.Errorf("a read with the page read-only: %v", meta)
	}
	if st, out := e.fs("GET", id, handle, "list?path="+q(dir), nil); st != 200 || out["files_read_only"] != true {
		t.Errorf("a list with the page read-only: %d %v", st, out)
	}
	if st, _ := e.fs("POST", id, handle, "download", map[string]any{"path": conf}); st != 201 {
		t.Errorf("a download with the page read-only: %d", st)
	}
}

// An sshd without the SFTP subsystem: the sign-in says so, and no session stays open.
func TestFilesNeedTheSFTPSubsystem(t *testing.T) {
	e := newEnv(t)
	e.enable()
	e.sshd.Files = nil
	st, out, _ := e.req("POST", "api/v1/sessions", sidAdmin, map[string]any{"kind": "files", "password": "root-pass"}, nil)
	if st != 502 || out["error"] != "no-sftp" {
		t.Fatalf("%d %v", st, out)
	}
	if n := e.mgr.Count(); n != 0 {
		t.Fatalf("%d sessions stayed", n)
	}
	// the password is checked first: a wrong one is a wrong password here too, and counts
	st, out, _ = e.req("POST", "api/v1/sessions", sidAdmin, map[string]any{"kind": "files", "password": "nope"}, nil)
	if st != 403 || out["error"] != "wrong-password" || out["tries_left"] != float64(4) {
		t.Fatalf("%d %v", st, out)
	}
}

// A file that never answers a read (the kernel has a few) would hold OpenSSH's SFTP server, and
// the session with it, for good: the session is ended instead, and says why.
func TestAFileThatNeverAnswersEndsTheSession(t *testing.T) {
	e := newEnv(t)
	e.enable()
	id, handle := e.openFiles(sidAdmin)
	release := make(chan struct{})
	defer close(release)
	e.tree.Fail = func(op, p string) error {
		if op == "Read" && p == "/proc/version" {
			<-release
		}
		return nil
	}
	st, out := e.fs("GET", id, handle, "read?path=/proc/version", nil)
	if st != 504 || out["error"] != "stuck" {
		t.Fatalf("%d %v", st, out)
	}
	if _, ok := e.mgr.Get(id); ok {
		t.Fatal("the session is still open")
	}
	if !e.audited("session-close id=" + id + " user=admin reason=stuck") {
		t.Fatalf("no audit line: %v", e.audit.List())
	}
}
