package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/hobbyquaker/ccu-addon-kit/go/addonkit"

	"github.com/openccu-lite/lite-addon-shell/internal/files"
	"github.com/openccu-lite/lite-addon-shell/internal/sessions"
)

// The files of a files session, under /api/v1/sessions/{id}/fs/.
//
// Every request carries the session's handle in the header X-Shell-Handle - never in the address,
// where a log, a history or a Referer would keep it - and comes from the web session that opened
// the session, over the same transport rules as the sign-in. A request is the user's doing: it
// resets the session's idle time. The daemon touches no file of the system itself: every path is
// cleaned and then handed to the system's SFTP server on the user's own SSH session (package
// files).
//
// A download cannot carry a header (the browser fetches it), so it is two steps: POST
// fs/download {path} answers a ticket, GET fs/download/{ticket} streams the file. The ticket is
// random, good for one use within 30 seconds, and only for the web session it was made for.
//
// The audit lines name who did what to which path - never a file's content.

// HandleHeader carries a files session's handle.
const HandleHeader = "X-Shell-Handle"

// MetaHeader carries a read file's description (JSON, ASCII only) beside its bytes.
const MetaHeader = "X-Shell-File"

const ticketFor = 30 * time.Second

type ticket struct {
	session string
	owner   string
	user    string
	path    string
	fs      *files.FS
	expires time.Time
}

func (s *server) fsRoutes(api *http.ServeMux) {
	const base = "/api/v1/sessions/{id}/fs/"
	handle(api, "GET "+base+"list", s.fsList)
	handle(api, "GET "+base+"stat", s.fsStat)
	handle(api, "GET "+base+"read", s.fsRead)
	handle(api, "GET "+base+"ids", s.fsIDs)
	handle(api, "GET "+base+"backups", s.fsBackups)
	handle(api, "PUT "+base+"write", s.fsWrite)
	handle(api, "POST "+base+"create", s.fsCreate)
	handle(api, "POST "+base+"mkdir", s.fsMkdir)
	handle(api, "POST "+base+"rename", s.fsRename)
	handle(api, "POST "+base+"remove", s.fsRemove)
	handle(api, "POST "+base+"chmod", s.fsChmod)
	handle(api, "POST "+base+"chown", s.fsChown)
	handle(api, "POST "+base+"upload", s.fsUpload)
	handle(api, "POST "+base+"restore", s.fsRestore)
	handle(api, "POST "+base+"download", s.fsTicket)
	handle(api, "GET "+base+"download/{ticket}", s.fsDownload)
}

// fsStatus is the HTTP status of a refusal of package files.
func fsStatus(code string) int {
	switch code {
	case "not-found":
		return http.StatusNotFound
	case "permission-denied", "read-only", "kernel", "backups", "is-link":
		return http.StatusForbidden
	case "too-large":
		return http.StatusRequestEntityTooLarge
	case "conflict", "exists", "not-empty":
		return http.StatusConflict
	case "precondition":
		return http.StatusPreconditionRequired
	case "no-space":
		return http.StatusInsufficientStorage
	case "invalid-path", "invalid", "not-a-file", "not-a-directory", "special":
		return http.StatusUnprocessableEntity
	}
	return http.StatusBadGateway
}

func fsFail(w http.ResponseWriter, err error) {
	var e *files.Error
	if !errors.As(err, &e) {
		e = &files.Error{Code: "failed", Message: err.Error()}
	}
	fail(w, fsStatus(e.Code), e.Code, e.Message, e.Extra)
}

// fsOf answers the files session a request is for, after the checks every such request passes:
// the transport, the web session that opened it, the handle. write also wants the setting that
// makes the files page read-only to be off.
func (s *server) fsOf(w http.ResponseWriter, r *http.Request, write bool) (*files.FS, caller, bool) {
	c := s.caller(r)
	if why := s.blocked(c); why != "" {
		fail(w, http.StatusForbidden, why, transportMessage(why), nil)
		return nil, c, false
	}
	conn, err := s.o.Sessions.Files(r.PathValue("id"), r.Header.Get(HandleHeader), c.who.SID)
	if err != nil {
		// one answer for "no such session", "not yours" and "not its handle"
		fail(w, http.StatusForbidden, "no-session", "no such session, or not its handle", nil)
		return nil, c, false
	}
	fsess, ok := conn.(*files.Session)
	if !ok {
		fail(w, http.StatusInternalServerError, "internal", "not a files session", nil)
		return nil, c, false
	}
	if write && s.o.Store.Settings().FilesReadOnly {
		fail(w, http.StatusForbidden, "read-only-setting", "the files page is set to read-only", nil)
		return nil, c, false
	}
	return fsess.FS, c, true
}

// fileAudit writes a file's audit line: the event, the session, the account, then the fields.
func (s *server) fileAudit(r *http.Request, c caller, event, format string, args ...any) {
	s.o.Audit.Printf("%s id=%s user=%s "+format, append([]any{event, auditToken(r.PathValue("id")), auditToken(c.who.User)}, args...)...)
}

// auditToken keeps a field one token.
func auditToken(v string) string {
	if v == "" {
		return "-"
	}
	return strings.Map(func(r rune) rune {
		if r <= ' ' || r == 0x7f || r == '=' || r == '"' {
			return '_'
		}
		return r
	}, v)
}

// asciiJSON is v as JSON without a byte outside ASCII, for a header.
func asciiJSON(v any) string {
	b, _ := json.Marshal(v)
	var out strings.Builder
	for _, r := range string(b) {
		switch {
		case r < 0x80:
			out.WriteRune(r)
		case r > 0xffff:
			r1, r2 := utf16.EncodeRune(r)
			fmt.Fprintf(&out, `\u%04x\u%04x`, r1, r2)
		default:
			fmt.Fprintf(&out, `\u%04x`, r)
		}
	}
	return out.String()
}

// GET fs/list?path=: a directory - {path, real, entries, truncated, writable, reason, mount,
// files_read_only}.
func (s *server) fsList(w http.ResponseWriter, r *http.Request) {
	fs, _, ok := s.fsOf(w, r, false)
	if !ok {
		return
	}
	l, err := fs.List(r.URL.Query().Get("path"))
	if err != nil {
		fsFail(w, err)
		return
	}
	writeJSON(w, 200, struct {
		*files.Listing
		FilesReadOnly bool `json:"files_read_only"`
	}{l, s.o.Store.Settings().FilesReadOnly})
}

// GET fs/stat?path=: one file, a link as itself.
func (s *server) fsStat(w http.ResponseWriter, r *http.Request) {
	fs, _, ok := s.fsOf(w, r, false)
	if !ok {
		return
	}
	i, err := fs.Stat(r.URL.Query().Get("path"))
	if err != nil {
		fsFail(w, err)
		return
	}
	writeJSON(w, 200, i)
}

// GET fs/ids: the system's accounts and groups, for the owner's choice.
func (s *server) fsIDs(w http.ResponseWriter, r *http.Request) {
	fs, _, ok := s.fsOf(w, r, false)
	if !ok {
		return
	}
	users, groups := fs.IDs()
	writeJSON(w, 200, map[string]any{"users": users, "groups": groups})
}

// GET fs/read?path=: a file for the editor. The body is the file's bytes (a binary file's first
// 64 KiB), the header X-Shell-File its description: kind, sha256, editable, why, size, mode, ...
func (s *server) fsRead(w http.ResponseWriter, r *http.Request) {
	fs, _, ok := s.fsOf(w, r, false)
	if !ok {
		return
	}
	// A few files of the kernel never answer a read (/proc/kmsg, a trace pipe), and OpenSSH's
	// SFTP server does one thing at a time: it would wait on that file for good, and every later
	// request with it. The session is ended instead, which ends the SFTP server too.
	type result struct {
		c   *files.Content
		err error
	}
	done := make(chan result, 1)
	go func() {
		c, err := fs.Read(r.URL.Query().Get("path"))
		done <- result{c, err}
	}()
	var c *files.Content
	select {
	case res := <-done:
		if res.err != nil {
			fsFail(w, res.err)
			return
		}
		c = res.c
	case <-time.After(s.o.ReadTimeout):
		s.o.Log("a file did not answer a read within %s: the files session ends", s.o.ReadTimeout)
		s.o.Sessions.End(r.PathValue("id"), sessions.ReasonStuck)
		fail(w, http.StatusGatewayTimeout, "stuck", "the file did not answer; the session was ended", nil)
		return
	}
	if c.Editable && s.o.Store.Settings().FilesReadOnly {
		c.Editable, c.Why = false, "setting"
	}
	w.Header().Set(MetaHeader, asciiJSON(c))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(c.Data)))
	w.WriteHeader(200)
	_, _ = w.Write(c.Data)
}

// PUT fs/write?path= with If-Match: "<sha256 of the content the editor opened>": saves the body
// over the file. 409 conflict {size, mtime, sha256} when the file is something else by now.
func (s *server) fsWrite(w http.ResponseWriter, r *http.Request) {
	fs, c, ok := s.fsOf(w, r, true)
	if !ok {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, files.MaxEdit+1))
	if err != nil {
		fsFail(w, &files.Error{Code: "too-large", Message: "the editor saves at most 2 MB", Extra: map[string]any{"max": files.MaxEdit}})
		return
	}
	saved, err := fs.Write(r.URL.Query().Get("path"), data, strings.Trim(r.Header.Get("If-Match"), `"`))
	if err != nil {
		fsFail(w, err)
		return
	}
	s.fileAudit(r, c, "file-write", "path=%q bytes=%d backup=%q in_place=%t", saved.Real, saved.Size, saved.Backup, saved.InPlace)
	writeJSON(w, 200, saved)
}

type pathBody struct {
	Path string `json:"path"`
}

// POST fs/create {path}: a new, empty file.
func (s *server) fsCreate(w http.ResponseWriter, r *http.Request) {
	fs, c, ok := s.fsOf(w, r, true)
	if !ok {
		return
	}
	var body pathBody
	if !decode(w, r, &body) {
		return
	}
	saved, err := fs.Create(body.Path)
	if err != nil {
		fsFail(w, err)
		return
	}
	s.fileAudit(r, c, "file-create", "path=%q", saved.Real)
	writeJSON(w, http.StatusCreated, saved)
}

// POST fs/mkdir {path}: a new directory.
func (s *server) fsMkdir(w http.ResponseWriter, r *http.Request) {
	fs, c, ok := s.fsOf(w, r, true)
	if !ok {
		return
	}
	var body pathBody
	if !decode(w, r, &body) {
		return
	}
	if err := fs.Mkdir(body.Path); err != nil {
		fsFail(w, err)
		return
	}
	s.fileAudit(r, c, "file-create", "path=%q dir=true", body.Path)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}

// POST fs/rename {from, to}: to a name that is free.
func (s *server) fsRename(w http.ResponseWriter, r *http.Request) {
	fs, c, ok := s.fsOf(w, r, true)
	if !ok {
		return
	}
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := fs.Rename(body.From, body.To); err != nil {
		fsFail(w, err)
		return
	}
	s.fileAudit(r, c, "file-rename", "from=%q to=%q", body.From, body.To)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// POST fs/remove {path, recursive}: 409 not-empty {entries} for a folder with something in it,
// unless recursive.
func (s *server) fsRemove(w http.ResponseWriter, r *http.Request) {
	fs, c, ok := s.fsOf(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if !decode(w, r, &body) {
		return
	}
	n, err := fs.Remove(body.Path, body.Recursive)
	if n > 0 {
		// also what a delete removed before it failed
		s.fileAudit(r, c, "file-delete", "path=%q entries=%d complete=%t", body.Path, n, err == nil)
	}
	if err != nil {
		fsFail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "removed": n})
}

// POST fs/chmod {path, mode}: mode as octal digits ("0644"). Never recursive.
func (s *server) fsChmod(w http.ResponseWriter, r *http.Request) {
	fs, c, ok := s.fsOf(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
	}
	if !decode(w, r, &body) {
		return
	}
	mode, err := strconv.ParseUint(body.Mode, 8, 32)
	if err != nil || len(body.Mode) > 4 {
		fsFail(w, &files.Error{Code: "invalid", Message: "a mode is up to four octal digits"})
		return
	}
	if err := fs.Chmod(body.Path, uint32(mode)); err != nil {
		fsFail(w, err)
		return
	}
	s.fileAudit(r, c, "file-chmod", "path=%q mode=%04o", body.Path, mode)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// POST fs/chown {path, uid, gid}. Never recursive.
func (s *server) fsChown(w http.ResponseWriter, r *http.Request) {
	fs, c, ok := s.fsOf(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Path string `json:"path"`
		UID  *int   `json:"uid"`
		GID  *int   `json:"gid"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.UID == nil || body.GID == nil {
		fsFail(w, &files.Error{Code: "invalid", Message: "uid and gid are needed"})
		return
	}
	if err := fs.Chown(body.Path, *body.UID, *body.GID); err != nil {
		fsFail(w, err)
		return
	}
	s.fileAudit(r, c, "file-chown", "path=%q uid=%d gid=%d", body.Path, *body.UID, *body.GID)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// POST fs/upload?path=&overwrite=1: the body is the file, streamed, at most 64 MB. 409 exists
// without overwrite.
func (s *server) fsUpload(w http.ResponseWriter, r *http.Request) {
	fs, c, ok := s.fsOf(w, r, true)
	if !ok {
		return
	}
	if r.ContentLength > files.MaxUpload {
		fsFail(w, &files.Error{Code: "too-large", Message: "an upload is at most 64 MB", Extra: map[string]any{"max": files.MaxUpload}})
		return
	}
	q := r.URL.Query()
	saved, err := fs.Upload(q.Get("path"), files.Limit(r.Body, files.MaxUpload), q.Get("overwrite") == "1")
	if err != nil {
		fsFail(w, err)
		return
	}
	s.fileAudit(r, c, "file-upload", "path=%q bytes=%d backup=%q", saved.Real, saved.Size, saved.Backup)
	writeJSON(w, http.StatusCreated, saved)
}

// GET fs/backups?path=: the kept versions of a file, newest first.
func (s *server) fsBackups(w http.ResponseWriter, r *http.Request) {
	fs, _, ok := s.fsOf(w, r, false)
	if !ok {
		return
	}
	list, err := fs.Backups(r.URL.Query().Get("path"))
	if err != nil {
		fsFail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"backups": list, "keep": files.KeepPerFile, "keep_bytes": files.KeepBytes, "root": fs.BackupRoot})
}

// POST fs/restore {path, name}: puts a kept version back; the present one is backed up first.
func (s *server) fsRestore(w http.ResponseWriter, r *http.Request) {
	fs, c, ok := s.fsOf(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	saved, err := fs.Restore(body.Path, body.Name)
	if err != nil {
		fsFail(w, err)
		return
	}
	s.fileAudit(r, c, "file-restore", "path=%q from=%q backup=%q", saved.Real, body.Name, saved.Backup)
	writeJSON(w, 200, saved)
}

// POST fs/download {path}: a ticket for one download of the file - {ticket, name, size}.
func (s *server) fsTicket(w http.ResponseWriter, r *http.Request) {
	fs, c, ok := s.fsOf(w, r, false)
	if !ok {
		return
	}
	var body pathBody
	if !decode(w, r, &body) {
		return
	}
	// opened once here, so a path that cannot be downloaded is refused where the page can show it
	rc, info, err := fs.Open(body.Path)
	if err != nil {
		fsFail(w, err)
		return
	}
	rc.Close()
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		addonkit.WriteError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	id := hex.EncodeToString(raw)
	now := time.Now()
	s.tmu.Lock()
	for k, t := range s.tickets {
		if now.After(t.expires) {
			delete(s.tickets, k)
		}
	}
	s.tickets[id] = ticket{session: r.PathValue("id"), owner: c.who.SID, user: c.who.User, path: info.Path, fs: fs, expires: now.Add(ticketFor)}
	s.tmu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"ticket": id, "name": info.Name, "size": info.Size})
}

// GET fs/download/{ticket}: the file, as an attachment. The ticket is spent by the first request,
// whoever makes it.
func (s *server) fsDownload(w http.ResponseWriter, r *http.Request) {
	c := s.caller(r)
	s.tmu.Lock()
	t, ok := s.tickets[r.PathValue("ticket")]
	delete(s.tickets, r.PathValue("ticket"))
	s.tmu.Unlock()
	info, alive := s.o.Sessions.Get(r.PathValue("id"))
	if !ok || time.Now().After(t.expires) || t.session != r.PathValue("id") || t.owner != c.who.SID ||
		!alive || info.Kind != sessions.KindFiles || info.Owner != c.who.SID || s.blocked(c) != "" {
		fail(w, http.StatusForbidden, "no-ticket", "this download link is spent or not yours", nil)
		return
	}
	rc, file, err := t.fs.Open(t.path)
	if err != nil {
		fsFail(w, err)
		return
	}
	defer rc.Close()
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Name}))
	if h.Get("Content-Disposition") == "" {
		h.Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(file.Name))
	}
	// whatever the file is, the browser stores it and runs nothing of it
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	if file.Reason != "kernel" {
		h.Set("Content-Length", strconv.FormatInt(file.Size, 10))
	}
	w.WriteHeader(200)
	n, _ := io.Copy(w, rc)
	s.o.Audit.Printf("file-download id=%s user=%s path=%q bytes=%d", auditToken(t.session), auditToken(t.user), file.Real, n)
}
