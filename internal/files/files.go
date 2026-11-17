// Package files is what a files session does on the system: listing, reading, saving, uploading,
// renaming, deleting, changing mode and owner, and the backups of every file it overwrites. All of
// it goes over SFTP on the user's own SSH session as root; the addon's process resolves no path
// and opens no file of the system itself. A path is cleaned here and then handed to the system's
// SFTP server, which is the one that follows links and checks what root may do.
//
// The package knows the system's rules that SFTP does not tell: which mounts are read-only (from
// /proc/mounts, read through the session), that /proc, /sys and /dev are not for an editor, and
// that the backups tree is written by saves only.
package files

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pkg/sftp"
)

// The limits of the editor and the transfers.
const (
	// MaxEdit is the largest file the editor saves.
	MaxEdit = 2 << 20
	// MaxView is the largest file the editor shows (read-only above MaxEdit).
	MaxView = 10 << 20
	// MaxKernel bounds a read below /proc and /sys, whose files have no size.
	MaxKernel = 1 << 20
	// HexView is how much of a binary file is shown.
	HexView = 64 << 10
	// MaxUpload is the largest upload.
	MaxUpload = 64 << 20
	// MaxEntries bounds one directory's listing.
	MaxEntries = 5000

	// DefaultBackupRoot is where the backups of overwritten files go: root's, outside the system
	// backup (.nobackup), kept when the addon is uninstalled.
	DefaultBackupRoot = "/usr/local/shell-backups"
	// KeepPerFile versions are kept of each file, KeepBytes in all.
	KeepPerFile = 10
	KeepBytes   = 50 << 20

	sniff = 8 << 10
)

// Error is a refusal with a code the page knows.
type Error struct {
	// Code is one of: invalid-path, not-found, permission-denied, read-only, kernel, backups,
	// not-a-file, not-a-directory, special, is-link, too-large, conflict, precondition, exists,
	// not-empty, no-space, backup-failed, invalid, failed.
	Code    string
	Message string
	// Extra travels in the answer (the file's state at a conflict, a folder's entries).
	Extra map[string]any
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func refuse(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Code answers an error's code, "failed" for one that is not an *Error.
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "failed"
}

// Clean checks and cleans a path a page sent: absolute, at most 4096 bytes, valid UTF-8, no
// control character. The answer has no "." or ".." element, no doubled and no trailing slash. It
// does not look at the system: links are the SFTP server's to follow.
func Clean(p string) (string, error) {
	if p == "" || len(p) > 4096 || p[0] != '/' || !utf8.ValidString(p) {
		return "", refuse("invalid-path", "a path is absolute, valid UTF-8 and at most 4096 bytes")
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "", refuse("invalid-path", "a path holds no control character")
		}
	}
	return path.Clean(p), nil
}

// under says whether p is dir or below it.
func under(p, dir string) bool {
	if dir == "/" {
		return true
	}
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// Entry is a file as a listing shows it. Times are Unix milliseconds.
type Entry struct {
	Name string `json:"name"`
	// Type is file, dir, link, device, fifo, socket or other.
	Type  string `json:"type"`
	Size  int64  `json:"size"`
	Mode  string `json:"mode"` // four octal digits
	UID   int    `json:"uid"`
	GID   int    `json:"gid"`
	Owner string `json:"owner"`
	Group string `json:"group"`
	Mtime int64  `json:"mtime"`
	// Target is a link's target as written, TargetType what it leads to ("" for a broken link).
	Target     string `json:"target,omitempty"`
	TargetType string `json:"target_type,omitempty"`
	// Locked is why the entry cannot be changed, as a Place's Reason; "" when it can. A listing
	// fills it in for what is not a link: a directory on another mount than its parent (the user
	// file system below the read-only root) is told apart there.
	Locked string `json:"locked,omitempty"`
}

// Place says whether a path can be changed, and why not.
type Place struct {
	Writable bool `json:"writable"`
	// Reason is "" or read-only (a read-only mount), kernel (/proc, /sys, /dev), backups (the
	// backups tree).
	Reason string `json:"reason"`
	// Mount is the mount point the path is on.
	Mount string `json:"mount"`
}

// Listing is a directory.
type Listing struct {
	Path string `json:"path"`
	// Real is the path with every link resolved.
	Real      string  `json:"real"`
	Entries   []Entry `json:"entries"`
	Truncated bool    `json:"truncated"`
	Place
}

// Info is one file with its place.
type Info struct {
	Entry
	Path string `json:"path"`
	Real string `json:"real"`
	Place
}

// Content is a file as the editor opens it.
type Content struct {
	Info
	// Kind is text or binary; a binary file's Data is its first HexView bytes.
	Kind      string `json:"kind"`
	SHA256    string `json:"sha256"`
	Truncated bool   `json:"truncated"`
	// Editable says whether the editor may save it, Why why not: binary, large, read-only,
	// kernel, backups.
	Editable bool   `json:"editable"`
	Why      string `json:"why"`
	Data     []byte `json:"-"`
}

// Saved is what a save, an upload or a restore did.
type Saved struct {
	Path   string `json:"path"`
	Real   string `json:"real"`
	Size   int64  `json:"size"`
	Mtime  int64  `json:"mtime"`
	Mode   string `json:"mode"`
	SHA256 string `json:"sha256,omitempty"`
	// Backup is where the replaced version went; "" when nothing was replaced or it was too large.
	Backup string `json:"backup"`
	// BackupSkipped is "too-large" when the replaced file was larger than all backups may be.
	BackupSkipped string `json:"backup_skipped,omitempty"`
	// InPlace says the file was written in place because it could not be replaced by a rename (a
	// bind-mounted file, a directory that takes no new file).
	InPlace bool `json:"in_place"`
	// Created says the file did not exist before.
	Created bool `json:"created"`
}

// Backup is one kept version.
type Backup struct {
	Name string `json:"name"`
	Path string `json:"path"`
	At   int64  `json:"at"`
	Size int64  `json:"size"`
}

// ID is an account or a group of the system.
type ID struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

type mount struct {
	point, fstype string
	ro            bool
}

// FS is a files session's view of the system.
type FS struct {
	// C is the SFTP client on the user's SSH session.
	C *sftp.Client
	// BackupRoot is DefaultBackupRoot unless a test names another.
	BackupRoot string
	// Now is the clock (a test's own).
	Now func() time.Time
	// Log takes what went wrong beside an operation (a prune that failed).
	Log func(format string, args ...any)
	// KeepVersions and KeepTotal are KeepPerFile and KeepBytes unless a test names less.
	KeepVersions int
	KeepTotal    int64

	// one save, upload, restore or delete at a time
	wmu sync.Mutex

	mu       sync.Mutex
	mounts   []mount
	mountsAt time.Time
	users    map[int]string
	groups   map[int]string
	idsAt    time.Time
}

// New makes an FS on an SFTP client.
func New(c *sftp.Client) *FS {
	return &FS{C: c, BackupRoot: DefaultBackupRoot, Now: time.Now, KeepVersions: KeepPerFile, KeepTotal: KeepBytes}
}

func (f *FS) logf(format string, args ...any) {
	if f.Log != nil {
		f.Log(format, args...)
	}
}

// explain turns an SFTP error into a refusal. OpenSSH's server names only "no such file" and
// "permission denied"; a full disk, a busy file and a read-only mount are all a bare failure, so
// the free space of dir is asked when one arrives.
func (f *FS) explain(err error, dir string) error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	var se *stageError
	switch {
	case errors.As(err, &se) && se.full:
		return refuse("no-space", "no space left on the file system")
	case errors.Is(err, os.ErrNotExist):
		return refuse("not-found", "no such file or directory")
	case errors.Is(err, os.ErrPermission):
		return refuse("permission-denied", "permission denied")
	}
	if dir != "" && f.full(dir) {
		return refuse("no-space", "no space left on the file system")
	}
	return refuse("failed", "the system refused: %v", err)
}

// full says whether the file system of dir has no room for another block of a file, as far as
// the server tells (OpenSSH's does).
func (f *FS) full(dir string) bool {
	v, err := f.C.StatVFS(dir)
	return err == nil && v.Bavail*v.Frsize < 32<<10
}

// real resolves every link of p on the system. A path that does not exist yet resolves through
// its directory.
func (f *FS) real(p string) (string, error) {
	r, err := f.C.RealPath(p)
	if err == nil {
		// a server that does not follow links answers the cleaned path: follow the last element
		return path.Clean(r), nil
	}
	if p == "/" {
		return "", f.explain(err, "")
	}
	dir, derr := f.C.RealPath(path.Dir(p))
	if derr != nil {
		return "", f.explain(derr, "")
	}
	return path.Join(dir, path.Base(p)), nil
}

func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func parseMounts(data []byte) []mount {
	var out []mount
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		m := mount{point: unescapeMount(fields[1]), fstype: fields[2]}
		for _, o := range strings.Split(fields[3], ",") {
			if o == "ro" {
				m.ro = true
			}
		}
		out = append(out, m)
	}
	return out
}

// readSmall reads a file of the system whole, up to max bytes.
func (f *FS) readSmall(p string, max int64) ([]byte, error) {
	file, err := f.C.Open(p)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	// LimitReader also keeps the client from sizing its reads by the file's stated size, which
	// is 0 below /proc
	return io.ReadAll(io.LimitReader(file, max))
}

func (f *FS) mountTable() []mount {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mounts != nil && f.Now().Sub(f.mountsAt) < 5*time.Second {
		return f.mounts
	}
	data, err := f.readSmall("/proc/mounts", MaxKernel)
	if err != nil {
		f.logf("the mounts could not be read: %v", err)
		return f.mounts
	}
	f.mounts, f.mountsAt = parseMounts(data), f.Now()
	return f.mounts
}

// place says whether the resolved path can be changed. Where the mounts cannot be read nothing is
// held back here: the system says no itself.
func (f *FS) place(real string) Place {
	for _, k := range []string{"/proc", "/sys", "/dev"} {
		if under(real, k) {
			return Place{Reason: "kernel", Mount: k}
		}
	}
	p := Place{Writable: true, Mount: "/"}
	best := -1
	for _, m := range f.mountTable() {
		// the last mount on a point is the one in force
		if under(real, m.point) && len(m.point) >= best {
			best = len(m.point)
			p.Mount = m.point
			p.Writable = !m.ro
		}
	}
	if !p.Writable {
		p.Reason = "read-only"
		return p
	}
	if under(real, f.BackupRoot) {
		return Place{Reason: "backups", Mount: p.Mount}
	}
	return p
}

// writable refuses a change at the resolved path with the place's reason.
func (f *FS) writable(real string) error {
	switch p := f.place(real); p.Reason {
	case "":
		return nil
	case "read-only":
		return refuse("read-only", "%s is on a read-only file system (%s)", real, p.Mount)
	case "kernel":
		return refuse("kernel", "%s belongs to the kernel and is not changed here", real)
	default:
		return refuse("backups", "the backups are written by saves only")
	}
}

func (f *FS) names() (map[int]string, map[int]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.users != nil && f.Now().Sub(f.idsAt) < 30*time.Second {
		return f.users, f.groups
	}
	parse := func(file string) map[int]string {
		out := map[int]string{}
		data, err := f.readSmall(file, 1<<20)
		if err != nil {
			return out
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Split(line, ":")
			if len(fields) < 3 || fields[0] == "" {
				continue
			}
			if id, err := strconv.Atoi(fields[2]); err == nil {
				if _, seen := out[id]; !seen {
					out[id] = fields[0]
				}
			}
		}
		return out
	}
	f.users, f.groups, f.idsAt = parse("/etc/passwd"), parse("/etc/group"), f.Now()
	return f.users, f.groups
}

// IDs lists the system's accounts and groups, by id.
func (f *FS) IDs() (users, groups []ID) {
	u, g := f.names()
	list := func(m map[int]string) []ID {
		out := make([]ID, 0, len(m))
		for id, name := range m {
			out = append(out, ID{Name: name, ID: id})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		return out
	}
	return list(u), list(g)
}

func typeOf(m os.FileMode) string {
	switch {
	case m.IsDir():
		return "dir"
	case m&os.ModeSymlink != 0:
		return "link"
	case m&os.ModeDevice != 0:
		return "device"
	case m&os.ModeNamedPipe != 0:
		return "fifo"
	case m&os.ModeSocket != 0:
		return "socket"
	case m.IsRegular():
		return "file"
	}
	return "other"
}

func unixMode(m os.FileMode) uint32 {
	u := uint32(m.Perm())
	if m&os.ModeSetuid != 0 {
		u |= 0o4000
	}
	if m&os.ModeSetgid != 0 {
		u |= 0o2000
	}
	if m&os.ModeSticky != 0 {
		u |= 0o1000
	}
	return u
}

func fileMode(u uint32) os.FileMode {
	m := os.FileMode(u & 0o777)
	if u&0o4000 != 0 {
		m |= os.ModeSetuid
	}
	if u&0o2000 != 0 {
		m |= os.ModeSetgid
	}
	if u&0o1000 != 0 {
		m |= os.ModeSticky
	}
	return m
}

func modeString(m os.FileMode) string { return fmt.Sprintf("%04o", unixMode(m)) }

func owner(fi os.FileInfo) (uid, gid int) {
	if st, ok := fi.Sys().(*sftp.FileStat); ok {
		return int(st.UID), int(st.GID)
	}
	return 0, 0
}

func (f *FS) entry(name string, fi os.FileInfo) Entry {
	users, groups := f.names()
	uid, gid := owner(fi)
	e := Entry{Name: name, Type: typeOf(fi.Mode()), Size: fi.Size(), Mode: modeString(fi.Mode()), UID: uid, GID: gid,
		Owner: users[uid], Group: groups[gid], Mtime: fi.ModTime().UnixMilli()}
	if e.Owner == "" {
		e.Owner = strconv.Itoa(uid)
	}
	if e.Group == "" {
		e.Group = strconv.Itoa(gid)
	}
	return e
}

// link fills in where a link leads.
func (f *FS) link(e *Entry, p string) {
	if e.Type != "link" {
		return
	}
	e.Target, _ = f.C.ReadLink(p)
	if fi, err := f.C.Stat(p); err == nil {
		e.TargetType = typeOf(fi.Mode())
	}
}

// List reads a directory. A link to a directory is followed: Real says where it led.
func (f *FS) List(p string) (*Listing, error) {
	p, err := Clean(p)
	if err != nil {
		return nil, err
	}
	fi, err := f.C.Stat(p)
	if err != nil {
		return nil, f.explain(err, "")
	}
	if !fi.IsDir() {
		return nil, refuse("not-a-directory", "%s is not a directory", p)
	}
	real, err := f.real(p)
	if err != nil {
		return nil, err
	}
	infos, err := f.C.ReadDir(real)
	if err != nil {
		return nil, f.explain(err, "")
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name() < infos[j].Name() })
	out := &Listing{Path: p, Real: real, Place: f.place(real), Entries: make([]Entry, 0, len(infos))}
	for _, fi := range infos {
		if fi.Name() == "." || fi.Name() == ".." {
			continue
		}
		if len(out.Entries) == MaxEntries {
			out.Truncated = true
			break
		}
		e := f.entry(fi.Name(), fi)
		f.link(&e, path.Join(real, fi.Name()))
		if e.Type != "link" {
			e.Locked = f.place(path.Join(real, fi.Name())).Reason
		}
		out.Entries = append(out.Entries, e)
	}
	return out, nil
}

// Stat describes one file; a link is described as itself, with where it leads.
func (f *FS) Stat(p string) (*Info, error) {
	p, err := Clean(p)
	if err != nil {
		return nil, err
	}
	fi, err := f.C.Lstat(p)
	if err != nil {
		return nil, f.explain(err, "")
	}
	e := f.entry(path.Base(p), fi)
	f.link(&e, p)
	// the place is the place of what a change would touch: the link itself lives in its directory
	dir, err := f.real(path.Dir(p))
	if err != nil {
		return nil, err
	}
	real := path.Join(dir, path.Base(p))
	if p == "/" {
		real = "/"
	}
	return &Info{Entry: e, Path: p, Real: real, Place: f.place(real)}, nil
}

// regular stats p through its links and wants a regular file. It answers the resolved path.
func (f *FS) regular(p string) (string, os.FileInfo, error) {
	fi, err := f.C.Stat(p)
	if err != nil {
		return "", nil, f.explain(err, "")
	}
	switch t := typeOf(fi.Mode()); t {
	case "file":
	case "dir":
		return "", nil, refuse("not-a-file", "%s is a directory", p)
	default:
		// a device, a FIFO or a socket is never opened: reading one can block or do something
		return "", nil, refuse("special", "%s is a %s and is not opened", p, t)
	}
	real, err := f.real(p)
	if err != nil {
		return "", nil, err
	}
	return real, fi, nil
}

// Binary says whether data is not text: a NUL byte in its first 8 KiB, or not valid UTF-8.
func Binary(data []byte) bool {
	head := data
	if len(head) > sniff {
		head = head[:sniff]
	}
	return bytes.IndexByte(head, 0) >= 0 || !utf8.Valid(data)
}

// wholeRunes drops a UTF-8 sequence that a cut left unfinished at the end.
func wholeRunes(data []byte) []byte {
	for i := 1; i <= utf8.UTFMax && i <= len(data); i++ {
		if b := data[len(data)-i]; b < 0x80 {
			break
		} else if b >= 0xc0 {
			// a lead byte: keep it only if its sequence is whole
			if !utf8.FullRune(data[len(data)-i:]) {
				return data[:len(data)-i]
			}
			break
		}
	}
	return data
}

// Read opens a file for the editor: its content if it is text of at most MaxView bytes, the first
// HexView bytes if it is binary. Files below /proc and /sys are read up to MaxKernel.
func (f *FS) Read(p string) (*Content, error) {
	p, err := Clean(p)
	if err != nil {
		return nil, err
	}
	real, fi, err := f.regular(p)
	if err != nil {
		return nil, err
	}
	place := f.place(real)
	max := int64(MaxView)
	if place.Reason == "kernel" {
		max = MaxKernel
	} else if fi.Size() > max {
		return nil, &Error{Code: "too-large", Message: "the file is too large for the editor", Extra: map[string]any{"size": fi.Size(), "max": max}}
	}
	data, err := f.readSmall(real, max+1)
	if err != nil {
		return nil, f.explain(err, "")
	}
	c := &Content{Info: Info{Entry: f.entry(path.Base(p), fi), Path: p, Real: real, Place: place}, Kind: "text"}
	if int64(len(data)) > max {
		if place.Reason != "kernel" {
			// it grew while it was read
			return nil, &Error{Code: "too-large", Message: "the file is too large for the editor", Extra: map[string]any{"size": len(data), "max": max}}
		}
		data, c.Truncated = data[:max], true
	}
	if c.Truncated {
		data = wholeRunes(data)
	}
	c.Size = int64(len(data))
	sum := sha256.Sum256(data)
	c.SHA256 = hex.EncodeToString(sum[:])
	switch {
	case Binary(data):
		c.Kind, c.Why = "binary", "binary"
		if len(data) > HexView {
			data, c.Truncated = data[:HexView], true
		}
	case place.Reason != "":
		c.Why = place.Reason
	case len(data) > MaxEdit:
		c.Why = "large"
	default:
		c.Editable = true
	}
	c.Data = data
	return c, nil
}

// Open opens a file for a download. Files of the kernel are cut at MaxKernel.
func (f *FS) Open(p string) (io.ReadCloser, *Info, error) {
	p, err := Clean(p)
	if err != nil {
		return nil, nil, err
	}
	real, fi, err := f.regular(p)
	if err != nil {
		return nil, nil, err
	}
	file, err := f.C.Open(real)
	if err != nil {
		return nil, nil, f.explain(err, "")
	}
	info := &Info{Entry: f.entry(path.Base(p), fi), Path: p, Real: real, Place: f.place(real)}
	if info.Reason == "kernel" {
		return struct {
			io.Reader
			io.Closer
		}{io.LimitReader(file, MaxKernel), file}, info, nil
	}
	// without WriterTo the copy reads until the end instead of trusting the size it was told
	return struct {
		io.Reader
		io.Closer
	}{file, file}, info, nil
}

func randomName() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// stageError says at which step a put failed: only a failure to make the file beside the target
// or to rename it into place is a reason to write in place instead - a failed write is a full
// disk, and writing in place would then empty the file.
type stageError struct {
	stage string // create, write, attr, rename
	err   error
	// full says the file system had no room left when the write failed (asked before the part
	// that was written is removed again, which makes room)
	full bool
}

func (e *stageError) Error() string { return e.stage + ": " + e.err.Error() }
func (e *stageError) Unwrap() error { return e.err }

// inPlaceWorthy says whether a failed put may be answered by writing in place.
func inPlaceWorthy(err error) bool {
	var se *stageError
	return errors.As(err, &se) && se.stage != "write"
}

func unsupported(err error) bool {
	var se *sftp.StatusError
	return errors.As(err, &se) && se.FxCode() == sftp.ErrSSHFxOpUnsupported
}

// put writes src to a new file beside real and renames it over real: nobody ever sees half a file
// under the real name. old is the file being replaced (nil for a new one, which gets mode): its
// mode and owner are the new file's. The temporary file is 0600 while it is written, so a secret
// is not readable on the way.
func (f *FS) put(real string, old os.FileInfo, mode os.FileMode, src io.Reader) (int64, error) {
	dir := path.Dir(real)
	tmp := path.Join(dir, "."+path.Base(real)+".shell-"+randomName())
	file, err := f.C.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return 0, &stageError{stage: "create", err: err}
	}
	fail := func(stage string, err error) (int64, error) {
		full := stage == "write" && f.full(dir)
		file.Close()
		_ = f.C.Remove(tmp)
		return 0, &stageError{stage: stage, err: err, full: full}
	}
	if err := f.C.Chmod(tmp, 0o600); err != nil {
		return fail("attr", err)
	}
	// io.Copy would take the client's ReadFrom; a plain loop keeps the writes in order and the
	// memory at one buffer
	n, err := io.CopyBuffer(struct{ io.Writer }{file}, src, make([]byte, 32<<10))
	if err != nil {
		return fail("write", err)
	}
	// fsync where the server has it (OpenSSH does); without it the rename is still atomic
	if err := file.Sync(); err != nil && !unsupported(err) {
		return fail("write", err)
	}
	if err := file.Close(); err != nil {
		full := f.full(dir)
		_ = f.C.Remove(tmp)
		return 0, &stageError{stage: "write", err: err, full: full}
	}
	if old != nil {
		// the owner first: a chown clears the set-id bits
		uid, gid := owner(old)
		if err := f.C.Chown(tmp, uid, gid); err != nil {
			_ = f.C.Remove(tmp)
			return 0, &stageError{stage: "attr", err: err}
		}
		mode = fileMode(unixMode(old.Mode()))
	}
	if err := f.C.Chmod(tmp, mode); err != nil {
		_ = f.C.Remove(tmp)
		return 0, &stageError{stage: "attr", err: err}
	}
	if err := f.C.PosixRename(tmp, real); err != nil {
		// a server without the extension: its own rename takes a free name only
		if old != nil || f.C.Rename(tmp, real) != nil {
			_ = f.C.Remove(tmp)
			return 0, &stageError{stage: "rename", err: err}
		}
	}
	return n, nil
}

// putInPlace overwrites real where it stands: for a file that cannot be replaced by a rename (a
// bind-mounted file, a directory that takes no new file). It looks at the free space first, as
// far as the server tells it: the file is emptied before it is written.
func (f *FS) putInPlace(real string, data []byte) error {
	if v, err := f.C.StatVFS(path.Dir(real)); err == nil && v.Bavail*v.Frsize < uint64(len(data)) {
		return refuse("no-space", "no space left on the file system")
	}
	file, err := f.C.OpenFile(real, os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil && !unsupported(err) {
		file.Close()
		return err
	}
	return file.Close()
}

func (f *FS) saved(p, real string) *Saved {
	s := &Saved{Path: p, Real: real}
	if fi, err := f.C.Stat(real); err == nil {
		s.Size, s.Mtime, s.Mode = fi.Size(), fi.ModTime().UnixMilli(), modeString(fi.Mode())
	}
	return s
}

// Write saves the editor's content over an existing file. ifMatch is the SHA-256 of the content
// the editor opened: when the file on the system is something else by now, nothing is written and
// the answer is a conflict with the file's state (the editor offers Overwrite, Reload, Compare; an
// overwrite sends the state it was shown). The replaced version goes to the backups first; without
// a backup there is no save. The new file has the old one's mode and owner.
func (f *FS) Write(p string, data []byte, ifMatch string) (*Saved, error) {
	p, err := Clean(p)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxEdit {
		return nil, &Error{Code: "too-large", Message: "the editor saves at most 2 MB", Extra: map[string]any{"max": MaxEdit}}
	}
	if len(ifMatch) != 64 {
		return nil, refuse("precondition", "a save names the SHA-256 of the content it replaces")
	}
	f.wmu.Lock()
	defer f.wmu.Unlock()
	real, old, err := f.regular(p)
	if err != nil {
		return nil, err
	}
	if err := f.writable(real); err != nil {
		return nil, err
	}
	conflict := func(size int64, sum string) error {
		return &Error{Code: "conflict", Message: "the file changed on the system since it was opened",
			Extra: map[string]any{"size": size, "mtime": old.ModTime().UnixMilli(), "sha256": sum}}
	}
	// one pass over the old content: into the backup, and through the hash that decides
	backup, sum, size, err := f.backup(real)
	if err != nil {
		return nil, err
	}
	if sum != strings.ToLower(ifMatch) {
		_ = f.C.Remove(backup)
		return nil, conflict(size, sum)
	}
	inPlace := false
	if _, err := f.put(real, old, 0, bytes.NewReader(data)); err != nil {
		if !inPlaceWorthy(err) {
			_ = f.C.Remove(backup) // nothing was replaced
			return nil, f.explain(err, path.Dir(real))
		}
		// no new file beside it (a bind-mounted file, a directory that takes none): in place
		if perr := f.putInPlace(real, data); perr != nil {
			// the file may be damaged now: the answer says where its last version is
			e := f.explain(perr, path.Dir(real)).(*Error)
			e.Extra = map[string]any{"backup": backup}
			return nil, e
		}
		inPlace = true
	}
	f.prune(real, backup)
	s := f.saved(p, real)
	sumNew := sha256.Sum256(data)
	s.SHA256, s.Backup, s.InPlace = hex.EncodeToString(sumNew[:]), backup, inPlace
	return s, nil
}

// Create makes a new, empty file (0644 root).
func (f *FS) Create(p string) (*Saved, error) {
	p, err := Clean(p)
	if err != nil {
		return nil, err
	}
	real, err := f.real(p)
	if err != nil {
		return nil, err
	}
	if err := f.writable(real); err != nil {
		return nil, err
	}
	if _, err := f.C.Lstat(real); err == nil {
		return nil, refuse("exists", "%s exists already", p)
	}
	file, err := f.C.OpenFile(real, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return nil, f.explain(err, path.Dir(real))
	}
	if err := file.Close(); err != nil {
		return nil, f.explain(err, path.Dir(real))
	}
	_ = f.C.Chmod(real, 0o644)
	s := f.saved(p, real)
	sum := sha256.Sum256(nil)
	s.SHA256, s.Created = hex.EncodeToString(sum[:]), true
	return s, nil
}

// Mkdir makes a directory (0755 root).
func (f *FS) Mkdir(p string) error {
	p, err := Clean(p)
	if err != nil {
		return err
	}
	real, err := f.real(p)
	if err != nil {
		return err
	}
	if err := f.writable(real); err != nil {
		return err
	}
	if _, err := f.C.Lstat(real); err == nil {
		return refuse("exists", "%s exists already", p)
	}
	if err := f.C.Mkdir(real); err != nil {
		return f.explain(err, path.Dir(real))
	}
	_ = f.C.Chmod(real, 0o755)
	return nil
}

// self resolves the directory of p and keeps its last element: a link is renamed, deleted and
// described as itself.
func (f *FS) self(p string) (string, error) {
	if p == "/" {
		return "/", nil
	}
	dir, err := f.real(path.Dir(p))
	if err != nil {
		return "", err
	}
	return path.Join(dir, path.Base(p)), nil
}

// Rename moves a file or a directory to a name that is free.
func (f *FS) Rename(from, to string) error {
	from, err := Clean(from)
	if err != nil {
		return err
	}
	if to, err = Clean(to); err != nil {
		return err
	}
	if from == "/" || to == "/" {
		return refuse("invalid", "the root directory is not renamed")
	}
	src, err := f.self(from)
	if err != nil {
		return err
	}
	dst, err := f.self(to)
	if err != nil {
		return err
	}
	if err := f.writable(src); err != nil {
		return err
	}
	if err := f.writable(dst); err != nil {
		return err
	}
	if under(dst, src) && dst != src {
		return refuse("invalid", "a directory is not moved into itself")
	}
	if _, err := f.C.Lstat(src); err != nil {
		return f.explain(err, "")
	}
	if _, err := f.C.Lstat(dst); err == nil {
		return refuse("exists", "%s exists already", to)
	}
	// SFTP's own rename, which takes a free name only: nothing is overwritten by a rename
	if err := f.C.Rename(src, dst); err != nil {
		return f.explain(err, path.Dir(dst))
	}
	return nil
}

// Remove deletes a file, a link (not what it leads to) or a directory. A directory that is not
// empty wants recursive: without it the answer is not-empty with the number of its entries.
func (f *FS) Remove(p string, recursive bool) (int, error) {
	p, err := Clean(p)
	if err != nil {
		return 0, err
	}
	if p == "/" {
		return 0, refuse("invalid", "the root directory is not deleted")
	}
	real, err := f.self(p)
	if err != nil {
		return 0, err
	}
	if real == f.BackupRoot {
		return 0, refuse("backups", "the backups tree itself stays; delete what is in it")
	}
	// inside the backups tree a delete is the one change that is allowed
	if pl := f.place(real); pl.Reason != "" && pl.Reason != "backups" {
		return 0, f.writable(real)
	}
	f.wmu.Lock()
	defer f.wmu.Unlock()
	fi, err := f.C.Lstat(real)
	if err != nil {
		return 0, f.explain(err, "")
	}
	if !fi.IsDir() {
		if err := f.C.Remove(real); err != nil {
			return 0, f.explain(err, "")
		}
		return 1, nil
	}
	if !recursive {
		entries, err := f.C.ReadDir(real)
		if err != nil {
			return 0, f.explain(err, "")
		}
		if len(entries) > 0 {
			return 0, &Error{Code: "not-empty", Message: "the directory is not empty", Extra: map[string]any{"entries": len(entries)}}
		}
	}
	n, err := f.removeAll(real, 0)
	if err != nil {
		return n, f.explain(err, "")
	}
	return n, nil
}

// removeAll deletes a directory with everything in it. Links are deleted, never followed.
func (f *FS) removeAll(dir string, depth int) (int, error) {
	if depth > 64 {
		return 0, refuse("failed", "the directory is nested too deeply")
	}
	entries, err := f.C.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.Name() == "." || e.Name() == ".." {
			continue
		}
		child := path.Join(dir, e.Name())
		if e.IsDir() {
			m, err := f.removeAll(child, depth+1)
			n += m
			if err != nil {
				return n, err
			}
			continue
		}
		if err := f.C.Remove(child); err != nil {
			return n, err
		}
		n++
	}
	if err := f.C.RemoveDirectory(dir); err != nil {
		return n, err
	}
	return n + 1, nil
}

// changeable resolves p for a change of mode or owner: not a link (the change would land on its
// target), and on a place that can be changed.
func (f *FS) changeable(p string) (string, os.FileInfo, error) {
	p, err := Clean(p)
	if err != nil {
		return "", nil, err
	}
	real, err := f.self(p)
	if err != nil {
		return "", nil, err
	}
	fi, err := f.C.Lstat(real)
	if err != nil {
		return "", nil, f.explain(err, "")
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", nil, refuse("is-link", "a link has no mode or owner of its own")
	}
	if err := f.writable(real); err != nil {
		return "", nil, err
	}
	return real, fi, nil
}

// Chmod sets a file's or a directory's mode (four octal digits at most). Never recursive.
func (f *FS) Chmod(p string, mode uint32) error {
	if mode > 0o7777 {
		return refuse("invalid", "a mode is at most 7777")
	}
	real, _, err := f.changeable(p)
	if err != nil {
		return err
	}
	return f.explain(f.C.Chmod(real, fileMode(mode)), "")
}

// Chown sets a file's or a directory's owner and group. Never recursive.
func (f *FS) Chown(p string, uid, gid int) error {
	if uid < 0 || gid < 0 || uid > 1<<31-1 || gid > 1<<31-1 {
		return refuse("invalid", "an owner and a group are numbers from 0")
	}
	real, _, err := f.changeable(p)
	if err != nil {
		return err
	}
	return f.explain(f.C.Chown(real, uid, gid), "")
}

// Upload stores src as p. An existing file is replaced only with overwrite, keeps its mode and
// owner, and goes to the backups first (unless it is larger than all backups may be). The content
// is streamed into a file beside p and renamed into place; an upload never writes in place.
func (f *FS) Upload(p string, src io.Reader, overwrite bool) (*Saved, error) {
	p, err := Clean(p)
	if err != nil {
		return nil, err
	}
	f.wmu.Lock()
	defer f.wmu.Unlock()
	real, err := f.real(p)
	if err != nil {
		return nil, err
	}
	if err := f.writable(real); err != nil {
		return nil, err
	}
	var old os.FileInfo
	backup, skipped := "", ""
	if fi, err := f.C.Stat(real); err == nil {
		if !fi.Mode().IsRegular() {
			return nil, refuse("not-a-file", "%s exists and is not a file", p)
		}
		if !overwrite {
			return nil, refuse("exists", "%s exists already", p)
		}
		old = fi
		if fi.Size() > f.KeepTotal {
			skipped = "too-large"
		} else if backup, _, _, err = f.backup(real); err != nil {
			return nil, err
		}
	}
	if _, err := f.put(real, old, 0o644, src); err != nil {
		var tooBig *maxBytesError
		if errors.As(err, &tooBig) {
			err = &Error{Code: "too-large", Message: "an upload is at most 64 MB", Extra: map[string]any{"max": MaxUpload}}
		}
		if backup != "" {
			_ = f.C.Remove(backup) // nothing was replaced
		}
		return nil, f.explain(err, path.Dir(real))
	}
	if backup != "" {
		f.prune(real, backup)
	}
	s := f.saved(p, real)
	s.Backup, s.BackupSkipped, s.Created = backup, skipped, old == nil
	return s, nil
}

// maxBytesError is what Limit's reader answers past its limit.
type maxBytesError struct{}

func (*maxBytesError) Error() string { return "too large" }

type limited struct {
	r    io.Reader
	left int64
}

func (l *limited) Read(p []byte) (int, error) {
	if l.left < 0 {
		return 0, &maxBytesError{}
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	if l.left < 0 {
		return 0, &maxBytesError{}
	}
	return n, err
}

// Limit answers a reader that fails past max bytes, so Upload refuses instead of storing a part.
func Limit(r io.Reader, max int64) io.Reader { return &limited{r: r, left: max} }
