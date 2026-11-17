package files

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The backups: every file a save, an upload or a restore replaces is copied to
// <BackupRoot>/<the file's directory>/<name>.<time> first, over the same SFTP session - so as
// root, into directories of mode 0700 and files of mode 0600. The addon's own user cannot read
// them between sessions (a backup of a file like shadow would otherwise leak to it), a .nobackup
// at the top keeps them out of the system backup, and the addon's uninstall leaves them where
// they are. The newest KeepPerFile versions of each file are kept, and KeepBytes in all.

const stampLayout = "20060102T150405Z"

var stampRE = regexp.MustCompile(`^\d{8}T\d{6}Z(-\d+)?$`)

// version splits a backup's name into the file's name and the time; ok is false for another file.
func version(name string) (base string, at time.Time, ok bool) {
	i := strings.LastIndexByte(name, '.')
	if i <= 0 || !stampRE.MatchString(name[i+1:]) {
		return "", time.Time{}, false
	}
	at, err := time.Parse(stampLayout, name[i+1:i+1+len(stampLayout)])
	if err != nil {
		return "", time.Time{}, false
	}
	return name[:i], at, true
}

func (f *FS) backupDir(real string) string { return path.Join(f.BackupRoot, path.Dir(real)) }

// ensureDir makes the backups directory for a file, every level of it 0700. A level that is not
// a directory (a link somebody put there) is refused: a backup never follows a link.
func (f *FS) ensureDir(dir string) error {
	if _, err := f.C.Lstat(f.BackupRoot); err != nil {
		if err := f.C.MkdirAll(f.BackupRoot); err != nil {
			return err
		}
	}
	rel := strings.Trim(strings.TrimPrefix(dir, f.BackupRoot), "/")
	cur := f.BackupRoot
	levels := []string{""}
	if rel != "" {
		levels = append(levels, strings.Split(rel, "/")...)
	}
	for _, l := range levels {
		cur = path.Join(cur, l)
		fi, err := f.C.Lstat(cur)
		switch {
		case err != nil:
			if err := f.C.Mkdir(cur); err != nil {
				return err
			}
			if err := f.C.Chmod(cur, 0o700); err != nil {
				return err
			}
		case !fi.IsDir():
			return fmt.Errorf("%s is not a directory", cur)
		case cur == f.BackupRoot && fi.Mode().Perm() != 0o700:
			if err := f.C.Chmod(cur, 0o700); err != nil {
				return err
			}
		}
	}
	// the system backup leaves a tree with this file out
	mark := path.Join(f.BackupRoot, ".nobackup")
	if _, err := f.C.Lstat(mark); err != nil {
		if file, err := f.C.OpenFile(mark, os.O_WRONLY|os.O_CREATE|os.O_EXCL); err == nil {
			file.Close()
			_ = f.C.Chmod(mark, 0o600)
		}
	}
	return nil
}

// backup copies the file at the resolved path to its backups directory and answers the copy's
// path, and the SHA-256 and the size of what was copied.
func (f *FS) backup(real string) (string, string, int64, error) {
	dir := f.backupDir(real)
	failed := func(err error) (string, string, int64, error) {
		e := f.explain(err, f.BackupRoot).(*Error)
		if e.Code != "no-space" {
			e = refuse("backup-failed", "the file's backup could not be written, so it was not replaced: %v", err)
		}
		return "", "", 0, e
	}
	if err := f.ensureDir(dir); err != nil {
		return failed(err)
	}
	src, err := f.C.Open(real)
	if err != nil {
		return "", "", 0, f.explain(err, "")
	}
	defer src.Close()
	stamp := f.Now().UTC().Format(stampLayout)
	var dst interface {
		io.WriteCloser
		Sync() error
	}
	var name string
	for i := 1; ; i++ {
		name = path.Join(dir, path.Base(real)+"."+stamp)
		if i > 1 {
			name += fmt.Sprintf("-%d", i)
		}
		file, err := f.C.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err == nil {
			dst = file
			break
		}
		// taken (two saves in one second): the next name; anything else is the end
		if _, serr := f.C.Lstat(name); serr != nil || i > 50 {
			return failed(err)
		}
	}
	drop := func(err error) (string, string, int64, error) {
		// asked before the half backup is removed, which makes room again
		full := f.full(dir)
		dst.Close()
		_ = f.C.Remove(name)
		if full {
			return "", "", 0, refuse("no-space", "no space left for the file's backup, so it was not replaced")
		}
		return failed(err)
	}
	if err := f.C.Chmod(name, 0o600); err != nil {
		return drop(err)
	}
	h := sha256.New()
	n, err := io.CopyBuffer(struct{ io.Writer }{io.MultiWriter(dst, h)}, struct{ io.Reader }{src}, make([]byte, 32<<10))
	if err != nil {
		return drop(err)
	}
	if err := dst.Sync(); err != nil && !unsupported(err) {
		return drop(err)
	}
	if err := dst.Close(); err != nil {
		_ = f.C.Remove(name)
		return failed(err)
	}
	return name, hex.EncodeToString(h.Sum(nil)), n, nil
}

// versions lists the backups of the file at the resolved path, newest first.
func (f *FS) versions(real string) []Backup {
	dir := f.backupDir(real)
	infos, err := f.C.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Backup
	for _, fi := range infos {
		base, at, ok := version(fi.Name())
		if !ok || base != path.Base(real) || !fi.Mode().IsRegular() {
			continue
		}
		out = append(out, Backup{Name: fi.Name(), Path: path.Join(dir, fi.Name()), At: at.UnixMilli(), Size: fi.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out
}

// prune keeps the newest KeepPerFile versions of the file and KeepBytes of backups in all; keep,
// the backup just made, always stays. What goes wrong here is logged and holds no save up.
func (f *FS) prune(real, keep string) {
	for i, b := range f.versions(real) {
		if i >= f.KeepVersions && b.Path != keep {
			if err := f.C.Remove(b.Path); err != nil {
				f.logf("an old backup could not be removed: %s: %v", b.Path, err)
			}
		}
	}
	type kept struct {
		path, stamp string
		size        int64
	}
	var all []kept
	var total int64
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > 64 || len(all) > 20000 {
			return
		}
		infos, err := f.C.ReadDir(dir)
		if err != nil {
			return
		}
		for _, fi := range infos {
			p := path.Join(dir, fi.Name())
			switch {
			case fi.IsDir():
				walk(p, depth+1)
			case fi.Mode().IsRegular():
				if _, _, ok := version(fi.Name()); ok {
					all = append(all, kept{path: p, stamp: fi.Name()[strings.LastIndexByte(fi.Name(), '.')+1:], size: fi.Size()})
					total += fi.Size()
				}
			}
		}
	}
	walk(f.BackupRoot, 0)
	if total <= f.KeepTotal {
		return
	}
	sort.Slice(all, func(i, j int) bool { return all[i].stamp < all[j].stamp })
	for _, k := range all {
		if total <= f.KeepTotal {
			break
		}
		if k.path == keep {
			continue
		}
		if err := f.C.Remove(k.path); err != nil {
			f.logf("an old backup could not be removed: %s: %v", k.path, err)
			continue
		}
		total -= k.size
	}
}

// Backups lists the kept versions of a file, newest first. The file itself need not exist any
// more.
func (f *FS) Backups(p string) ([]Backup, error) {
	p, err := Clean(p)
	if err != nil {
		return nil, err
	}
	real, err := f.real(p)
	if err != nil {
		return nil, err
	}
	out := f.versions(real)
	if out == nil {
		out = []Backup{}
	}
	return out, nil
}

// Restore puts a kept version back as the file. The file's present version goes to the backups
// first, like at a save; a file that is gone is made again, as 0600 root (what it was is not
// known). name is the backup's file name, as Backups lists it.
func (f *FS) Restore(p, name string) (*Saved, error) {
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
	base, _, ok := version(name)
	if !ok || base != path.Base(real) || strings.ContainsRune(name, '/') {
		return nil, refuse("invalid", "%s is not a backup of this file", name)
	}
	if err := f.writable(real); err != nil {
		return nil, err
	}
	from := path.Join(f.backupDir(real), name)
	bi, err := f.C.Lstat(from)
	if err != nil {
		return nil, f.explain(err, "")
	}
	if !bi.Mode().IsRegular() {
		return nil, refuse("invalid", "%s is not a backup of this file", name)
	}
	var old os.FileInfo
	backup := ""
	if fi, err := f.C.Stat(real); err == nil {
		if !fi.Mode().IsRegular() {
			return nil, refuse("not-a-file", "%s exists and is not a file", p)
		}
		old = fi
		if backup, _, _, err = f.backup(real); err != nil {
			return nil, err
		}
	}
	src, err := f.C.Open(from)
	if err != nil {
		return nil, f.explain(err, "")
	}
	_, err = f.put(real, old, 0o600, struct{ io.Reader }{src})
	src.Close()
	inPlace := false
	if err != nil {
		if old == nil || !inPlaceWorthy(err) || bi.Size() > MaxView {
			if backup != "" {
				_ = f.C.Remove(backup)
			}
			return nil, f.explain(err, path.Dir(real))
		}
		data, rerr := f.readSmall(from, MaxView)
		if rerr != nil {
			return nil, f.explain(rerr, "")
		}
		if perr := f.putInPlace(real, data); perr != nil {
			var e *Error
			if !errors.As(f.explain(perr, path.Dir(real)), &e) {
				e = refuse("failed", "%v", perr)
			}
			e.Extra = map[string]any{"backup": backup}
			return nil, e
		}
		inPlace = true
	}
	if backup != "" {
		f.prune(real, backup)
	}
	s := f.saved(p, real)
	s.Backup, s.InPlace, s.Created = backup, inPlace, old == nil
	if s.Size <= MaxView {
		if data, err := f.readSmall(real, MaxView+1); err == nil {
			sum := sha256.Sum256(data)
			s.SHA256 = hex.EncodeToString(sum[:])
		}
	}
	return s, nil
}
