package files_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/openccu-lite/lite-addon-shell/internal/fakesshd"
	"github.com/openccu-lite/lite-addon-shell/internal/files"
)

// The tests run against an SFTP server in this process over a scratch directory seeded like a
// small openccu-lite system (fakesshd.Seed): / is read-only by /proc/mounts, /usr/local is not,
// /etc/config is the link into it. The server reports every file as root's and remembers an owner
// that is set; a test makes single operations fail the way a real system does.

type env struct {
	t    *testing.T
	fs   *files.FS
	tree *fakesshd.Tree
	root string
	now  time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, root: t.TempDir(), now: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
	if err := fakesshd.Seed(e.root); err != nil {
		t.Fatal(err)
	}
	e.tree = fakesshd.NewTree(e.root)
	c, stop, err := e.tree.Client()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	e.fs = files.New(c)
	// a second later at every look: two backups never share a name
	e.fs.Now = func() time.Time {
		e.now = e.now.Add(time.Second)
		return e.now
	}
	return e
}

func (e *env) real(p string) string { return filepath.Join(e.root, filepath.FromSlash(p)) }

func (e *env) content(p string) string {
	e.t.Helper()
	b, err := os.ReadFile(e.real(p))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func (e *env) put(p, content string) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(e.real(p)), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(e.real(p), []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) mode(p string) os.FileMode {
	e.t.Helper()
	fi, err := os.Lstat(e.real(p))
	if err != nil {
		e.t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want %s", code)
	}
	if got := files.Code(err); got != code {
		t.Fatalf("error %q (%v), want %s", got, err, code)
	}
}

func (e *env) backups(p string) []string {
	e.t.Helper()
	entries, _ := os.ReadDir(filepath.Join(e.real(files.DefaultBackupRoot), filepath.FromSlash(path.Dir(p))))
	var out []string
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), path.Base(p)+".") {
			out = append(out, en.Name())
		}
	}
	return out
}

const conf = "/usr/local/etc/config/rfd.conf"

func TestCleanTakesAbsolutePathsOnlyAndLeavesNoDotElement(t *testing.T) {
	for in, want := range map[string]string{
		"/":                       "/",
		"/usr/local":              "/usr/local",
		"/usr/local/":             "/usr/local",
		"//usr//local":            "/usr/local",
		"/usr/local/../../etc":    "/etc",
		"/../../..":               "/",
		"/usr/./local/etc/..":     "/usr/local",
		"/with space/ä":           "/with space/ä",
		"/usr/local/etc/config/.": "/usr/local/etc/config",
	} {
		got, err := files.Clean(in)
		if err != nil || got != want {
			t.Errorf("Clean(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "usr/local", "../etc", "/a\x00b", "/a\nb", "/a\x7fb", "/\xff\xfe", "/" + strings.Repeat("a", 4096)} {
		if got, err := files.Clean(in); err == nil {
			t.Errorf("Clean(%q) = %q, want a refusal", in, got)
		} else {
			wantCode(t, err, "invalid-path")
		}
	}
}

func FuzzCleanNeverLeavesTheRootOrADotElement(f *testing.F) {
	for _, s := range []string{"/", "/a/../..", "//a//b/", "/a/./b", "a", "", "/a\x00", "/..a/b..", "/a/b/../../../c", "/\xe2\x80\xae/x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out, err := files.Clean(in)
		if err != nil {
			return
		}
		if !strings.HasPrefix(out, "/") {
			t.Fatalf("Clean(%q) = %q: not absolute", in, out)
		}
		if out != "/" && strings.HasSuffix(out, "/") {
			t.Fatalf("Clean(%q) = %q: a trailing slash", in, out)
		}
		for _, el := range strings.Split(out, "/")[1:] {
			if el == "." || el == ".." || (el == "" && out != "/") {
				t.Fatalf("Clean(%q) = %q: element %q", in, out, el)
			}
		}
		if strings.ContainsAny(out, "\x00\n\r") {
			t.Fatalf("Clean(%q) = %q: a control character", in, out)
		}
		// cleaning what is clean changes nothing
		if again, err := files.Clean(out); err != nil || again != out {
			t.Fatalf("Clean(Clean(%q)) = %q, %v; want %q", in, again, err, out)
		}
	})
}

func TestListShowsADirectoryWithOwnersLinksAndWhetherItCanBeChanged(t *testing.T) {
	e := newEnv(t)
	e.tree.SetOwner(conf, 30014, 30014)
	l, err := e.fs.List("/usr/local/etc/config")
	if err != nil {
		t.Fatal(err)
	}
	if !l.Writable || l.Reason != "" || l.Mount != "/usr/local" {
		t.Fatalf("the user file system: %+v", l.Place)
	}
	by := map[string]files.Entry{}
	for i, en := range l.Entries {
		by[en.Name] = en
		if i > 0 && l.Entries[i-1].Name >= en.Name {
			t.Fatalf("not sorted: %q before %q", l.Entries[i-1].Name, en.Name)
		}
	}
	if en := by["rfd.conf"]; en.Type != "file" || en.Mode != "0644" || en.Owner != "addon-shell" || en.Group != "addon-shell" || en.UID != 30014 || en.Size == 0 || en.Mtime == 0 {
		t.Fatalf("rfd.conf: %+v", en)
	}
	if en := by["crontab"]; en.Owner != "root" || en.Group != "root" {
		t.Fatalf("crontab: %+v", en)
	}
	if en := by["empty"]; en.Type != "dir" {
		t.Fatalf("empty: %+v", en)
	}
	if en := by["broken"]; en.Type != "link" || en.Target != "/usr/local/etc/config/gone" || en.TargetType != "" {
		t.Fatalf("the broken link: %+v", en)
	}
	if en := by["hosts-link"]; en.Type != "link" || en.TargetType != "file" {
		t.Fatalf("the link to a file: %+v", en)
	}
	// the root file system is read-only, and says so
	l, err = e.fs.List("/etc")
	if err != nil {
		t.Fatal(err)
	}
	if l.Writable || l.Reason != "read-only" || l.Mount != "/" {
		t.Fatalf("/etc: %+v", l.Place)
	}
	// what is in a read-only directory is locked, but for a mount that is not
	if l, err = e.fs.List("/usr"); err != nil || l.Writable || len(l.Entries) != 1 || l.Entries[0].Name != "local" || l.Entries[0].Locked != "" {
		t.Fatalf("/usr: %+v, %v", l, err)
	}
	if l, err = e.fs.List("/etc"); err != nil || l.Entries[0].Name != "config" || l.Entries[0].Locked != "" || l.Entries[1].Locked != "read-only" {
		t.Fatalf("/etc's entries: %+v, %v", l.Entries, err)
	}
	// /etc/config is a link into the user file system: listed there, and it can be changed
	l, err = e.fs.List("/etc/config")
	if err != nil {
		t.Fatal(err)
	}
	if l.Real != "/usr/local/etc/config" || !l.Writable {
		t.Fatalf("/etc/config: real %q, %+v", l.Real, l.Place)
	}
	if l, err = e.fs.List("/proc"); err != nil || l.Reason != "kernel" {
		t.Fatalf("/proc: %+v, %v", l, err)
	}
	_, err = e.fs.List(conf)
	wantCode(t, err, "not-a-directory")
	_, err = e.fs.List("/nowhere")
	wantCode(t, err, "not-found")
	_, err = e.fs.List("relative")
	wantCode(t, err, "invalid-path")
}

func TestStatDescribesALinkAsItself(t *testing.T) {
	e := newEnv(t)
	i, err := e.fs.Stat("/etc/config")
	if err != nil {
		t.Fatal(err)
	}
	// the link stands in /etc, on the read-only root: it cannot be renamed or deleted
	if i.Type != "link" || i.Target != "/usr/local/etc/config" || i.TargetType != "dir" || i.Real != "/etc/config" || i.Writable {
		t.Fatalf("%+v", i)
	}
	if i, err = e.fs.Stat("/"); err != nil || i.Type != "dir" || i.Writable {
		t.Fatalf("/: %+v, %v", i, err)
	}
}

func TestReadOpensTextForEditingAndSaysWhyAFileIsNotEditable(t *testing.T) {
	e := newEnv(t)
	c, err := e.fs.Read(conf)
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != "text" || !c.Editable || c.Why != "" || string(c.Data) != e.content(conf) || c.SHA256 != sum(e.content(conf)) || c.Real != conf {
		t.Fatalf("%+v", c)
	}
	// through the link the file is the same file
	if c, err = e.fs.Read("/etc/config/rfd.conf"); err != nil || c.Real != conf || !c.Editable {
		t.Fatalf("through the link: %+v, %v", c, err)
	}
	// on the read-only root: shown, not editable
	if c, err = e.fs.Read("/etc/hostname"); err != nil || c.Editable || c.Why != "read-only" || string(c.Data) != "openccu-lite\n" {
		t.Fatalf("/etc/hostname: %+v, %v", c, err)
	}
	// the kernel's files have no size and are never editable
	if c, err = e.fs.Read("/proc/version"); err != nil || c.Editable || c.Why != "kernel" || !strings.HasPrefix(string(c.Data), "Linux") {
		t.Fatalf("/proc/version: %+v, %v", c, err)
	}
	// a binary file: its first bytes, for a hex view
	if c, err = e.fs.Read("/usr/local/etc/config/firmware.bin"); err != nil || c.Kind != "binary" || c.Editable || c.Why != "binary" {
		t.Fatalf("firmware.bin: %+v, %v", c, err)
	}
	e.put("/usr/local/big.bin", strings.Repeat("\x00\x01", files.HexView))
	if c, err = e.fs.Read("/usr/local/big.bin"); err != nil || len(c.Data) != files.HexView || !c.Truncated {
		t.Fatalf("big.bin: %d bytes, %v", len(c.Data), err)
	}
	// text that is not UTF-8 is binary to the editor: it would be saved as something else
	e.put("/usr/local/latin1.txt", "gr\xfc\xdfe\n")
	if c, err = e.fs.Read("/usr/local/latin1.txt"); err != nil || c.Kind != "binary" {
		t.Fatalf("latin1: %+v, %v", c, err)
	}
	// over 2 MB: shown, not editable; over 10 MB: not opened
	e.put("/usr/local/large.log", strings.Repeat("line\n", files.MaxEdit/5+1))
	if c, err = e.fs.Read("/usr/local/large.log"); err != nil || c.Editable || c.Why != "large" || c.Kind != "text" {
		t.Fatalf("large.log: %+v, %v", c.Info, err)
	}
	e.put("/usr/local/huge.log", strings.Repeat("0123456789abcde\n", files.MaxView/16+1))
	_, err = e.fs.Read("/usr/local/huge.log")
	wantCode(t, err, "too-large")
	_, err = e.fs.Read("/usr/local/etc/config")
	wantCode(t, err, "not-a-file")
	_, err = e.fs.Read("/usr/local/etc/config/broken")
	wantCode(t, err, "not-found")
}

func TestTheKernelsFilesThatNeverAnswerAreNotOpened(t *testing.T) {
	e := newEnv(t)
	e.put("/proc/kmsg", "would block on a system\n")
	e.put("/sys/kernel/tracing/trace_pipe", "would block too\n")
	e.put("/sys/kernel/tracing/per_cpu/cpu0/trace_pipe_raw", "and this\n")
	e.put("/sys/kernel/tracing/trace", "this one answers\n")
	opened := ""
	e.tree.Fail = func(op, p string) error {
		if op == "Read" || op == "Open" {
			opened += p + " "
		}
		return nil
	}
	for _, p := range []string{"/proc/kmsg", "/sys/kernel/tracing/trace_pipe", "/sys/kernel/tracing/per_cpu/cpu0/trace_pipe_raw"} {
		_, err := e.fs.Read(p)
		wantCode(t, err, "special")
		_, _, err = e.fs.Open(p)
		wantCode(t, err, "special")
	}
	if opened != "" {
		t.Fatalf("opened: %s", opened)
	}
	if c, err := e.fs.Read("/sys/kernel/tracing/trace"); err != nil || string(c.Data) != "this one answers\n" {
		t.Fatalf("%+v, %v", c, err)
	}
}

func TestAFifoIsNeverOpened(t *testing.T) {
	e := newEnv(t)
	if err := syscall.Mkfifo(e.real("/usr/local/pipe"), 0o644); err != nil {
		t.Skip("no FIFO here:", err)
	}
	opened := false
	e.tree.Fail = func(op, p string) error {
		if (op == "Read" || op == "Open") && strings.HasSuffix(p, "/pipe") {
			opened = true
		}
		return nil
	}
	_, err := e.fs.Read("/usr/local/pipe")
	wantCode(t, err, "special")
	_, _, err = e.fs.Open("/usr/local/pipe")
	wantCode(t, err, "special")
	if opened {
		t.Fatal("the FIFO was opened")
	}
}

func TestASaveKeepsModeAndOwnerAndBacksUpWhatItReplaces(t *testing.T) {
	e := newEnv(t)
	old := e.content(conf)
	if err := os.Chmod(e.real(conf), 0o640); err != nil {
		t.Fatal(err)
	}
	e.tree.SetOwner(conf, 30014, 33)
	s, err := e.fs.Write(conf, []byte("Listen Port = 32002\n"), sum(old))
	if err != nil {
		t.Fatal(err)
	}
	if e.content(conf) != "Listen Port = 32002\n" || s.InPlace || s.Size != 20 || s.SHA256 != sum("Listen Port = 32002\n") || s.Mode != "0640" {
		t.Fatalf("%+v, content %q", s, e.content(conf))
	}
	if e.mode(conf) != 0o640 {
		t.Fatalf("mode %o, want 0640", e.mode(conf))
	}
	if uid, gid := e.tree.Owner(conf); uid != 30014 || gid != 33 {
		t.Fatalf("owner %d:%d, want 30014:33", uid, gid)
	}
	// the backup: the old content, in root's tree, closed to everyone else, out of the system backup
	if !strings.HasPrefix(s.Backup, files.DefaultBackupRoot+"/usr/local/etc/config/rfd.conf.2026") {
		t.Fatalf("backup at %q", s.Backup)
	}
	if e.content(s.Backup) != old {
		t.Fatalf("the backup holds %q", e.content(s.Backup))
	}
	if e.mode(s.Backup) != 0o600 {
		t.Fatalf("the backup's mode %o", e.mode(s.Backup))
	}
	for _, d := range []string{files.DefaultBackupRoot, files.DefaultBackupRoot + "/usr", files.DefaultBackupRoot + "/usr/local/etc/config"} {
		if e.mode(d) != 0o700 {
			t.Fatalf("%s: mode %o, want 0700", d, e.mode(d))
		}
	}
	if _, err := os.Stat(e.real(files.DefaultBackupRoot + "/.nobackup")); err != nil {
		t.Fatal("no .nobackup at the top of the backups")
	}
	// nothing is left beside the file
	entries, _ := os.ReadDir(e.real("/usr/local/etc/config"))
	for _, en := range entries {
		if strings.Contains(en.Name(), ".shell-") {
			t.Fatalf("a temporary file stayed: %s", en.Name())
		}
	}
	// a save through the link lands on the same file and in the same backups
	s2, err := e.fs.Write("/etc/config/rfd.conf", []byte("x\n"), s.SHA256)
	if err != nil || s2.Real != conf || path.Dir(s2.Backup) != path.Dir(s.Backup) {
		t.Fatalf("through the link: %+v, %v", s2, err)
	}
}

func TestASaveOverAFileThatChangedIsAConflictAndWritesNothing(t *testing.T) {
	e := newEnv(t)
	opened := e.content(conf)
	e.put(conf, "changed by someone else\n")
	_, err := e.fs.Write(conf, []byte("mine\n"), sum(opened))
	wantCode(t, err, "conflict")
	x := err.(*files.Error).Extra
	if x["sha256"] != sum("changed by someone else\n") || x["size"] != int64(24) || x["mtime"] == nil {
		t.Fatalf("the conflict's answer: %+v", x)
	}
	if e.content(conf) != "changed by someone else\n" {
		t.Fatal("the file was written")
	}
	if b := e.backups(conf); len(b) != 0 {
		t.Fatalf("a conflict left a backup: %v", b)
	}
	// "Overwrite" names the state the conflict showed
	s, err := e.fs.Write(conf, []byte("mine\n"), x["sha256"].(string))
	if err != nil || e.content(conf) != "mine\n" || e.content(s.Backup) != "changed by someone else\n" {
		t.Fatalf("%+v, %v", s, err)
	}
	// a save without the state it replaces is refused
	_, err = e.fs.Write(conf, []byte("x"), "")
	wantCode(t, err, "precondition")
}

func TestAFileThatCannotBeReplacedIsWrittenInPlace(t *testing.T) {
	// a bind-mounted file: the rename over it fails
	e := newEnv(t)
	old := e.content(conf)
	e.tree.Fail = func(op, p string) error {
		if op == "PosixRename" {
			return syscall.EBUSY
		}
		return nil
	}
	s, err := e.fs.Write(conf, []byte("in place\n"), sum(old))
	if err != nil || !s.InPlace || e.content(conf) != "in place\n" || e.content(s.Backup) != old {
		t.Fatalf("%+v, %v", s, err)
	}
	entries, _ := os.ReadDir(e.real("/usr/local/etc/config"))
	for _, en := range entries {
		if strings.Contains(en.Name(), ".shell-") {
			t.Fatalf("the temporary file stayed: %s", en.Name())
		}
	}
	// a directory that takes no new file
	e.tree.Fail = func(op, p string) error {
		if op == "Open" && strings.Contains(p, ".shell-") {
			return syscall.EACCES
		}
		return nil
	}
	s, err = e.fs.Write(conf, []byte("again\n"), s.SHA256)
	if err != nil || !s.InPlace || e.content(conf) != "again\n" {
		t.Fatalf("%+v, %v", s, err)
	}
}

func TestAFullDiskFailsTheSaveAndLeavesTheFileAlone(t *testing.T) {
	e := newEnv(t)
	old := e.content(conf)
	e.tree.NoSpace = true
	e.tree.Fail = func(op, p string) error {
		if op == "Write" && strings.Contains(p, ".shell-") {
			return syscall.ENOSPC
		}
		return nil
	}
	_, err := e.fs.Write(conf, []byte("does not fit\n"), sum(old))
	wantCode(t, err, "no-space")
	if e.content(conf) != old {
		t.Fatalf("the file was touched: %q", e.content(conf))
	}
	if b := e.backups(conf); len(b) != 0 {
		t.Fatalf("a backup of a save that did not happen: %v", b)
	}
	// and when the backup itself does not fit, nothing is replaced either
	e.tree.Fail = func(op, p string) error {
		if op == "Write" && strings.HasPrefix(p, files.DefaultBackupRoot) {
			return syscall.ENOSPC
		}
		return nil
	}
	_, err = e.fs.Write(conf, []byte("x\n"), sum(old))
	wantCode(t, err, "no-space")
	if e.content(conf) != old || len(e.backups(conf)) != 0 {
		t.Fatal("the file or a half backup stayed")
	}
	// in place is not tried on a full disk: it would empty the file first
	e.tree.Fail = func(op, p string) error {
		if op == "PosixRename" {
			return syscall.EBUSY
		}
		return nil
	}
	_, err = e.fs.Write(conf, []byte("x\n"), sum(old))
	wantCode(t, err, "no-space")
	if e.content(conf) != old {
		t.Fatalf("the file was emptied: %q", e.content(conf))
	}
}

func TestSavesAreRefusedWhereTheSystemIsNotToBeChanged(t *testing.T) {
	e := newEnv(t)
	_, err := e.fs.Write("/etc/hostname", []byte("x\n"), sum("openccu-lite\n"))
	wantCode(t, err, "read-only")
	_, err = e.fs.Write("/proc/version", []byte("x\n"), sum(e.content("/proc/version")))
	wantCode(t, err, "kernel")
	_, err = e.fs.Write(conf, bytes.Repeat([]byte("a"), files.MaxEdit+1), sum(e.content(conf)))
	wantCode(t, err, "too-large")
	_, err = e.fs.Write("/usr/local/etc/config", []byte("x"), sum(""))
	wantCode(t, err, "not-a-file")
	_, err = e.fs.Write("/usr/local/nothing-here", []byte("x"), sum(""))
	wantCode(t, err, "not-found")
	// a backup is not edited
	s, err := e.fs.Write(conf, []byte("x\n"), sum(e.content(conf)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.fs.Write(s.Backup, []byte("forged\n"), sum(e.content(s.Backup)))
	wantCode(t, err, "backups")
	_, err = e.fs.Create(files.DefaultBackupRoot + "/new")
	wantCode(t, err, "backups")
	_, err = e.fs.Upload(files.DefaultBackupRoot+"/new", strings.NewReader("x"), false)
	wantCode(t, err, "backups")
	if c, err := e.fs.Read(s.Backup); err != nil || c.Editable || c.Why != "backups" {
		t.Fatalf("a backup read: %+v, %v", c, err)
	}
}

func TestOldBackupsArePrunedByCountAndBySize(t *testing.T) {
	e := newEnv(t)
	e.fs.KeepVersions = 3
	last := sum(e.content(conf))
	var made []string
	for i := 0; i < 6; i++ {
		s, err := e.fs.Write(conf, []byte(strings.Repeat("v", i+1)), last)
		if err != nil {
			t.Fatal(err)
		}
		last = s.SHA256
		made = append(made, path.Base(s.Backup))
	}
	got := e.backups(conf)
	if len(got) != 3 || got[0] != made[3] || got[2] != made[5] {
		t.Fatalf("kept %v of %v, want the newest three", got, made)
	}
	// the size in all: the oldest go first, over all files, and the one just made stays
	e.fs.KeepVersions = 10
	e.fs.KeepTotal = 2010
	other := "/usr/local/etc/config/crontab"
	lastOther := sum(e.content(other))
	for i := 0; i < 3; i++ {
		s, err := e.fs.Write(other, bytes.Repeat([]byte("x"), 1000), lastOther)
		if err != nil {
			t.Fatal(err)
		}
		lastOther = s.SHA256
	}
	if got := e.backups(conf); len(got) != 0 {
		t.Fatalf("the other file's old backups stayed over the limit: %v", got)
	}
	if got := e.backups(other); len(got) != 2 {
		t.Fatalf("crontab's backups: %v, want the two that fit", got)
	}
	// one backup larger than the limit is still kept: it is the last version of that file
	e.fs.KeepTotal = 10
	s, err := e.fs.Write(other, []byte("small"), lastOther)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.backups(other); len(got) != 1 || got[0] != path.Base(s.Backup) {
		t.Fatalf("after the small limit: %v, want only %s", got, path.Base(s.Backup))
	}
}

func TestBackupsAreListedNewestFirstAndRestored(t *testing.T) {
	e := newEnv(t)
	v1 := e.content(conf)
	s1, err := e.fs.Write(conf, []byte("v2\n"), sum(v1))
	if err != nil {
		t.Fatal(err)
	}
	s2, err := e.fs.Write(conf, []byte("v3\n"), s1.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	list, err := e.fs.Backups(conf)
	if err != nil || len(list) != 2 || list[0].Path != s2.Backup || list[1].Path != s1.Backup || list[1].Size != int64(len(v1)) || list[0].At <= list[1].At {
		t.Fatalf("%+v, %v", list, err)
	}
	if err := os.Chmod(e.real(conf), 0o640); err != nil {
		t.Fatal(err)
	}
	// a restore replaces like a save: the present version is backed up, mode and owner stay
	r, err := e.fs.Restore(conf, list[1].Name)
	if err != nil {
		t.Fatal(err)
	}
	if e.content(conf) != v1 || e.content(r.Backup) != "v3\n" || e.mode(conf) != 0o640 || r.SHA256 != sum(v1) || r.Created {
		t.Fatalf("%+v, content %q", r, e.content(conf))
	}
	// a file that is gone comes back, closed: what it was is not known
	if err := os.Remove(e.real(conf)); err != nil {
		t.Fatal(err)
	}
	if list, err = e.fs.Backups(conf); err != nil || len(list) != 3 {
		t.Fatalf("the backups of a deleted file: %+v, %v", list, err)
	}
	r, err = e.fs.Restore(conf, list[0].Name)
	if err != nil || !r.Created || e.content(conf) != "v3\n" || e.mode(conf) != 0o600 {
		t.Fatalf("%+v, %v, mode %o", r, err, e.mode(conf))
	}
	// only a backup of this very file
	for _, name := range []string{"../../../../../etc/passwd", "crontab.20261117T120000Z", "rfd.conf", "rfd.conf.20261117T120000Z/..", "rfd.conf.20991231T000000Z"} {
		if _, err := e.fs.Restore(conf, name); err == nil {
			t.Fatalf("Restore(%q) worked", name)
		}
	}
	if list, err = e.fs.Backups("/usr/local/etc/config/crontab"); err != nil || len(list) != 0 || list == nil {
		t.Fatalf("a file without backups: %#v, %v", list, err)
	}
}

func TestUploadStreamsIntoPlaceAndAsksBeforeItReplaces(t *testing.T) {
	e := newEnv(t)
	s, err := e.fs.Upload("/usr/local/new.bin", strings.NewReader("\x00\x01\x02"), false)
	if err != nil || !s.Created || s.Size != 3 || s.Backup != "" || e.content("/usr/local/new.bin") != "\x00\x01\x02" || e.mode("/usr/local/new.bin") != 0o644 {
		t.Fatalf("%+v, %v", s, err)
	}
	_, err = e.fs.Upload("/usr/local/new.bin", strings.NewReader("again"), false)
	wantCode(t, err, "exists")
	if err := os.Chmod(e.real("/usr/local/new.bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.tree.SetOwner("/usr/local/new.bin", 33, 33)
	s, err = e.fs.Upload("/usr/local/new.bin", strings.NewReader("again"), true)
	if err != nil || s.Created || e.content("/usr/local/new.bin") != "again" || e.mode("/usr/local/new.bin") != 0o755 || e.content(s.Backup) != "\x00\x01\x02" {
		t.Fatalf("%+v, %v", s, err)
	}
	if uid, _ := e.tree.Owner("/usr/local/new.bin"); uid != 33 {
		t.Fatalf("owner %d", uid)
	}
	// past the limit: refused, nothing new, the old file as it was, no backup of a replace that did not happen
	before := len(e.backups("/usr/local/new.bin"))
	_, err = e.fs.Upload("/usr/local/new.bin", files.Limit(strings.NewReader(strings.Repeat("x", 100)), 10), true)
	wantCode(t, err, "too-large")
	if e.content("/usr/local/new.bin") != "again" || len(e.backups("/usr/local/new.bin")) != before {
		t.Fatal("a refused upload changed something")
	}
	entries, _ := os.ReadDir(e.real("/usr/local"))
	for _, en := range entries {
		if strings.Contains(en.Name(), ".shell-") {
			t.Fatalf("a part of the upload stayed: %s", en.Name())
		}
	}
	_, err = e.fs.Upload("/etc/new", strings.NewReader("x"), false)
	wantCode(t, err, "read-only")
	_, err = e.fs.Upload("/usr/local/etc", strings.NewReader("x"), true)
	wantCode(t, err, "not-a-file")
	// a file larger than all backups may be is replaced without one, and the answer says so
	e.fs.KeepTotal = 3
	s, err = e.fs.Upload("/usr/local/new.bin", strings.NewReader("third"), true)
	if err != nil || s.Backup != "" || s.BackupSkipped != "too-large" {
		t.Fatalf("%+v, %v", s, err)
	}
}

func TestDownloadOpensAFileOnly(t *testing.T) {
	e := newEnv(t)
	r, info, err := e.fs.Open("/media/usb1/space in name.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var b bytes.Buffer
	if _, err := b.ReadFrom(r); err != nil || b.String() != "spaces\n" || info.Name != "space in name.txt" || info.Size != 7 {
		t.Fatalf("%q, %+v, %v", b.String(), info, err)
	}
	_, _, err = e.fs.Open("/media/usb1")
	wantCode(t, err, "not-a-file")
}

func TestCreateMkdirRenameAndRemove(t *testing.T) {
	e := newEnv(t)
	s, err := e.fs.Create("/usr/local/etc/config/new.conf")
	if err != nil || !s.Created || s.SHA256 != sum("") || e.content("/usr/local/etc/config/new.conf") != "" {
		t.Fatalf("%+v, %v", s, err)
	}
	_, err = e.fs.Create("/usr/local/etc/config/new.conf")
	wantCode(t, err, "exists")
	_, err = e.fs.Create("/etc/new.conf")
	wantCode(t, err, "read-only")
	_, err = e.fs.Create("/usr/local/no/such/dir/x")
	wantCode(t, err, "not-found")
	if err := e.fs.Mkdir("/usr/local/etc/config/sub"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, e.fs.Mkdir("/usr/local/etc/config/sub"), "exists")
	wantCode(t, e.fs.Mkdir("/etc/sub"), "read-only")

	if err := e.fs.Rename("/usr/local/etc/config/new.conf", "/usr/local/etc/config/sub/moved.conf"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.real("/usr/local/etc/config/sub/moved.conf")); err != nil {
		t.Fatal(err)
	}
	// a rename never overwrites
	wantCode(t, e.fs.Rename("/usr/local/etc/config/crontab", conf), "exists")
	wantCode(t, e.fs.Rename("/usr/local/etc/config/sub", "/usr/local/etc/config/sub/inner"), "invalid")
	wantCode(t, e.fs.Rename("/etc/hostname", "/usr/local/hostname"), "read-only")
	wantCode(t, e.fs.Rename(conf, "/etc/rfd.conf"), "read-only")
	wantCode(t, e.fs.Rename("/usr/local/gone", "/usr/local/there"), "not-found")
	wantCode(t, e.fs.Rename("/", "/x"), "invalid")
	// a link is renamed as itself
	if err := e.fs.Rename("/usr/local/etc/config/hosts-link", "/usr/local/etc/config/hl"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(e.real("/usr/local/etc/config/hl")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link did not move as a link")
	}

	// a delete: a link goes, not what it leads to
	if n, err := e.fs.Remove("/usr/local/etc/config/hl", false); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if e.content("/etc/hostname") != "openccu-lite\n" {
		t.Fatal("the link's target was deleted")
	}
	// a folder that is not empty says how much is in it and stays
	e.put("/usr/local/etc/config/sub/deep/er/file", "x")
	if err := os.Symlink("/etc", e.real("/usr/local/etc/config/sub/to-etc")); err != nil {
		t.Fatal(err)
	}
	_, err = e.fs.Remove("/usr/local/etc/config/sub", false)
	wantCode(t, err, "not-empty")
	if n := err.(*files.Error).Extra["entries"]; n != 3 {
		t.Fatalf("entries %v, want 3", n)
	}
	// with everything in it: the link inside is removed, not followed
	if n, err := e.fs.Remove("/usr/local/etc/config/sub", true); err != nil || n != 6 {
		t.Fatal(n, err)
	}
	if _, err := os.Stat(e.real("/usr/local/etc/config/sub")); !os.IsNotExist(err) {
		t.Fatal("the folder is still there")
	}
	if e.content("/etc/passwd") == "" {
		t.Fatal("the delete went through the link")
	}
	if n, err := e.fs.Remove("/usr/local/etc/config/empty", false); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	_, err = e.fs.Remove("/", true)
	wantCode(t, err, "invalid")
	_, err = e.fs.Remove("/etc/hostname", false)
	wantCode(t, err, "read-only")
	_, err = e.fs.Remove("/usr/local/gone", false)
	wantCode(t, err, "not-found")
	// a backup may be deleted, the tree itself not
	s, err = e.fs.Write(conf, []byte("x\n"), sum(e.content(conf)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.fs.Remove(s.Backup, false); err != nil {
		t.Fatal(err)
	}
	_, err = e.fs.Remove(files.DefaultBackupRoot, true)
	wantCode(t, err, "backups")
}

func TestModeAndOwnerAreSetOnTheFileItselfNeverThroughALink(t *testing.T) {
	e := newEnv(t)
	if err := e.fs.Chmod(conf, 0o4750); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(e.real(conf)); fi.Mode().Perm() != 0o750 || fi.Mode()&os.ModeSetuid == 0 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if i, err := e.fs.Stat(conf); err != nil || i.Mode != "4750" {
		t.Fatalf("%+v, %v", i, err)
	}
	if err := e.fs.Chown(conf, 33, 65534); err != nil {
		t.Fatal(err)
	}
	if i, err := e.fs.Stat(conf); err != nil || i.Owner != "www-data" || i.Group != "nogroup" {
		t.Fatalf("%+v, %v", i, err)
	}
	// an id without a name is shown as the number
	if err := e.fs.Chown(conf, 4242, 0); err != nil {
		t.Fatal(err)
	}
	if i, _ := e.fs.Stat(conf); i.Owner != "4242" || i.Group != "root" {
		t.Fatalf("%+v", i)
	}
	wantCode(t, e.fs.Chmod("/usr/local/etc/config/hosts-link", 0o600), "is-link")
	wantCode(t, e.fs.Chown("/usr/local/etc/config/hosts-link", 0, 0), "is-link")
	if e.mode("/etc/hostname") != 0o644 {
		t.Fatal("the change went through the link")
	}
	wantCode(t, e.fs.Chmod("/etc/hostname", 0o600), "read-only")
	wantCode(t, e.fs.Chown("/etc/hostname", 0, 0), "read-only")
	wantCode(t, e.fs.Chmod(conf, 0o10000), "invalid")
	wantCode(t, e.fs.Chown(conf, -1, 0), "invalid")
	wantCode(t, e.fs.Chmod("/usr/local/gone", 0o600), "not-found")
	users, groups := e.fs.IDs()
	if len(users) != 5 || users[0].Name != "root" || users[3].ID != 30014 || len(groups) != 5 {
		t.Fatalf("%+v %+v", users, groups)
	}
}

func TestAMountPointWithASpaceAndALaterMountOnTheSamePoint(t *testing.T) {
	e := newEnv(t)
	e.put("/proc/mounts", "/dev/root / ext4 ro 0 0\n/dev/sda3 /usr/local ext4 rw 0 0\n"+
		"/dev/sdc1 /media/my\\040stick vfat ro 0 0\n/dev/sdd1 /usr/local ext4 ro 0 0\n")
	if err := os.MkdirAll(e.real("/media/my stick"), 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := e.fs.List("/media/my stick")
	if err != nil || l.Mount != "/media/my stick" || l.Writable {
		t.Fatalf("%+v, %v", l, err)
	}
	// the mount that was made last on a point is the one in force
	if l, err = e.fs.List("/usr/local"); err != nil || l.Writable {
		t.Fatalf("%+v, %v", l, err)
	}
}
