package fakesshd

import (
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/sftp"
)

// Tree is the fake sshd's file system for its SFTP subsystem: a scratch directory that the SFTP
// client sees as "/". Absolute symlinks resolve inside it, every file is reported as root's (an
// owner set over SFTP is remembered, not applied - the tests do not run as root), and a test can
// make single operations fail the way a real system does (a bind-mounted file that cannot be
// renamed over, a full disk). Nothing outside the directory is reachable.
type Tree struct {
	// Root is the scratch directory.
	Root string
	// Fail, when set, is asked before every operation with its name ("Open", "Write", "Read",
	// "Rename", "PosixRename", "Setstat", "Remove", "Rmdir", "Mkdir", "Symlink", "List", "Stat",
	// "Lstat", "Readlink") and the path as the client names it; an error it answers is the
	// operation's result. OpenSSH answers most refusals as a bare failure: return syscall.EBUSY and
	// the like, which travel the same way.
	Fail func(op, path string) error
	// NoSpace makes the file system report no free block (statvfs).
	NoSpace bool

	mu     sync.Mutex
	owners map[string][2]uint32
}

// NewTree makes a Tree over dir.
func NewTree(dir string) *Tree { return &Tree{Root: dir, owners: map[string][2]uint32{}} }

// Handlers are the SFTP handlers over the tree (sftp.NewRequestServer).
func (t *Tree) Handlers() sftp.Handlers {
	return sftp.Handlers{FileGet: t, FilePut: t, FileCmd: t, FileList: t}
}

// Owner answers the owner remembered for a path (0, 0 by default).
func (t *Tree) Owner(p string) (uid, gid uint32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	o := t.owners[path.Clean("/"+p)]
	return o[0], o[1]
}

// SetOwner remembers an owner, as a chown over SFTP does.
func (t *Tree) SetOwner(p string, uid, gid uint32) {
	t.mu.Lock()
	t.owners[path.Clean("/"+p)] = [2]uint32{uid, gid}
	t.mu.Unlock()
}

func (t *Tree) fail(op, p string) error {
	if t.Fail == nil {
		return nil
	}
	return t.Fail(op, p)
}

// resolve walks p inside the tree as the kernel would inside a chroot: a symlink's absolute
// target starts at the tree's root again. followLast says whether the last element is followed.
// It answers the path as the client would name it (no link left in it, but for an unfollowed last
// element) and the real path below Root.
func (t *Tree) resolve(p string, followLast bool) (string, string, error) {
	rest := strings.Split(strings.Trim(path.Clean("/"+p), "/"), "/")
	cur := "/"
	for hops := 0; len(rest) > 0; {
		name := rest[0]
		rest = rest[1:]
		if name == "" {
			continue
		}
		next := path.Join(cur, name)
		fi, err := os.Lstat(filepath.Join(t.Root, next))
		if err != nil {
			if os.IsNotExist(err) && len(rest) == 0 {
				// the last element may be missing: a file about to be made
				return next, filepath.Join(t.Root, next), nil
			}
			return "", "", err
		}
		if fi.Mode()&os.ModeSymlink != 0 && (len(rest) > 0 || followLast) {
			if hops++; hops > 40 {
				return "", "", syscall.ELOOP
			}
			target, err := os.Readlink(filepath.Join(t.Root, next))
			if err != nil {
				return "", "", err
			}
			if !path.IsAbs(target) {
				target = path.Join(cur, target)
			}
			rest = append(strings.Split(strings.Trim(path.Clean(target), "/"), "/"), rest...)
			cur = "/"
			continue
		}
		cur = next
	}
	return cur, filepath.Join(t.Root, cur), nil
}

// info is a file's attributes as the fake reports them: root's unless an owner was set.
type info struct {
	os.FileInfo
	name     string
	uid, gid uint32
}

func (i info) Name() string { return i.name }
func (i info) Uid() uint32  { return i.uid }
func (i info) Gid() uint32  { return i.gid }
func (i info) Sys() any     { return nil }

func (t *Tree) info(virtual string, fi os.FileInfo) info {
	uid, gid := t.Owner(virtual)
	return info{FileInfo: fi, name: fi.Name(), uid: uid, gid: gid}
}

type lister []os.FileInfo

func (l lister) ListAt(out []os.FileInfo, off int64) (int, error) {
	if off >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(out, l[off:])
	if n < len(out) {
		return n, io.EOF
	}
	return n, nil
}

// Filelist serves List and Stat (and Readlink where Readlink below is not asked).
func (t *Tree) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	if err := t.fail(r.Method, r.Filepath); err != nil {
		return nil, err
	}
	virtual, real, err := t.resolve(r.Filepath, true)
	if err != nil {
		return nil, err
	}
	switch r.Method {
	case "List":
		entries, err := os.ReadDir(real)
		if err != nil {
			return nil, err
		}
		out := make(lister, 0, len(entries))
		for _, e := range entries {
			fi, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, t.info(path.Join(virtual, e.Name()), fi))
		}
		return out, nil
	default:
		fi, err := os.Lstat(real)
		if err != nil {
			return nil, err
		}
		return lister{t.info(virtual, fi)}, nil
	}
}

// Lstat is Stat without following the last element.
func (t *Tree) Lstat(r *sftp.Request) (sftp.ListerAt, error) {
	if err := t.fail("Lstat", r.Filepath); err != nil {
		return nil, err
	}
	virtual, real, err := t.resolve(r.Filepath, false)
	if err != nil {
		return nil, err
	}
	fi, err := os.Lstat(real)
	if err != nil {
		return nil, err
	}
	return lister{t.info(virtual, fi)}, nil
}

// Readlink answers a link's target as it is written.
func (t *Tree) Readlink(p string) (string, error) {
	if err := t.fail("Readlink", p); err != nil {
		return "", err
	}
	_, real, err := t.resolve(p, false)
	if err != nil {
		return "", err
	}
	return os.Readlink(real)
}

// RealPath resolves every link, as OpenSSH's server does; a missing path is an error there too.
func (t *Tree) RealPath(p string) (string, error) {
	virtual, real, err := t.resolve(p, true)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(real); err != nil {
		return "", err
	}
	return virtual, nil
}

// Fileread opens a file for reading.
func (t *Tree) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	if err := t.fail("Read", r.Filepath); err != nil {
		return nil, err
	}
	_, real, err := t.resolve(r.Filepath, true)
	if err != nil {
		return nil, err
	}
	return os.Open(real)
}

func (t *Tree) open(r *sftp.Request) (*os.File, error) {
	if err := t.fail("Open", r.Filepath); err != nil {
		return nil, err
	}
	_, real, err := t.resolve(r.Filepath, true)
	if err != nil {
		return nil, err
	}
	fl := r.Pflags()
	flags := os.O_WRONLY
	if fl.Read {
		flags = os.O_RDWR
	}
	if fl.Creat {
		flags |= os.O_CREATE
	}
	if fl.Trunc {
		flags |= os.O_TRUNC
	}
	if fl.Excl {
		flags |= os.O_EXCL
	}
	return os.OpenFile(real, flags, 0o644)
}

// failingWriter asks Fail before every write (a disk that fills up while a file is written).
type failingWriter struct {
	*os.File
	t    *Tree
	path string
}

func (w failingWriter) WriteAt(p []byte, off int64) (int, error) {
	if err := w.t.fail("Write", w.path); err != nil {
		return 0, err
	}
	return w.File.WriteAt(p, off)
}

// Filewrite opens a file for writing.
func (t *Tree) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	f, err := t.open(r)
	if err != nil {
		return nil, err
	}
	return failingWriter{File: f, t: t, path: r.Filepath}, nil
}

// OpenFile opens a file for reading and writing.
func (t *Tree) OpenFile(r *sftp.Request) (sftp.WriterAtReaderAt, error) {
	f, err := t.open(r)
	if err != nil {
		return nil, err
	}
	return failingWriter{File: f, t: t, path: r.Filepath}, nil
}

func (t *Tree) moveOwner(from, to string) {
	t.mu.Lock()
	if o, ok := t.owners[from]; ok {
		t.owners[to] = o
		delete(t.owners, from)
	} else {
		delete(t.owners, to)
	}
	t.mu.Unlock()
}

// PosixRename replaces the target, as rename(2) does.
func (t *Tree) PosixRename(r *sftp.Request) error {
	if err := t.fail("PosixRename", r.Target); err != nil {
		return err
	}
	fromV, from, err := t.resolve(r.Filepath, false)
	if err != nil {
		return err
	}
	toV, to, err := t.resolve(r.Target, false)
	if err != nil {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		return err
	}
	t.moveOwner(fromV, toV)
	return nil
}

// Filecmd serves the operations that answer only success or failure.
func (t *Tree) Filecmd(r *sftp.Request) error {
	if err := t.fail(r.Method, r.Filepath); err != nil {
		return err
	}
	switch r.Method {
	case "Setstat":
		virtual, real, err := t.resolve(r.Filepath, true)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(real); err != nil {
			return err
		}
		fl, a := r.AttrFlags(), r.Attributes()
		if fl.Size {
			if err := os.Truncate(real, int64(a.Size)); err != nil {
				return err
			}
		}
		if fl.UidGid {
			t.SetOwner(virtual, a.UID, a.GID)
		}
		if fl.Permissions {
			if err := os.Chmod(real, a.FileMode().Perm()|a.FileMode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky)); err != nil {
				return err
			}
		}
		if fl.Acmodtime {
			return os.Chtimes(real, time.Unix(int64(a.Atime), 0), time.Unix(int64(a.Mtime), 0))
		}
		return nil
	case "Rename":
		// SFTP's own rename refuses an existing target
		fromV, from, err := t.resolve(r.Filepath, false)
		if err != nil {
			return err
		}
		toV, to, err := t.resolve(r.Target, false)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(to); err == nil {
			return syscall.EEXIST
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
		t.moveOwner(fromV, toV)
		return nil
	case "Rmdir", "Remove":
		virtual, real, err := t.resolve(r.Filepath, false)
		if err != nil {
			return err
		}
		fi, err := os.Lstat(real)
		if err != nil {
			return err
		}
		if (r.Method == "Rmdir") != fi.IsDir() {
			return syscall.ENOTDIR
		}
		if err := os.Remove(real); err != nil {
			return err
		}
		t.mu.Lock()
		delete(t.owners, virtual)
		t.mu.Unlock()
		return nil
	case "Mkdir":
		_, real, err := t.resolve(r.Filepath, false)
		if err != nil {
			return err
		}
		return os.Mkdir(real, 0o755)
	case "Symlink":
		// Filepath is the target as written, Target the link's place
		_, link, err := t.resolve(r.Target, false)
		if err != nil {
			return err
		}
		return os.Symlink(r.Filepath, link)
	}
	return errors.New("not supported by the fake")
}

// StatVFS answers the free space: plenty, or none with NoSpace.
func (t *Tree) StatVFS(r *sftp.Request) (*sftp.StatVFS, error) {
	free := uint64(1 << 20)
	if t.NoSpace {
		free = 0
	}
	return &sftp.StatVFS{Bsize: 4096, Frsize: 4096, Blocks: 1 << 21, Bfree: free, Bavail: free, Files: 1 << 20, Ffree: 1 << 19, Favail: 1 << 19, Namemax: 255}, nil
}

type pipeConn struct {
	io.Reader
	io.WriteCloser
}

// Client answers an SFTP client on the tree without an SSH connection around it (a test of what
// is done over SFTP), and the function that ends it.
func (t *Tree) Client() (*sftp.Client, func(), error) {
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	srv := sftp.NewRequestServer(pipeConn{sr, sw}, t.Handlers())
	go func() { _ = srv.Serve() }()
	c, err := sftp.NewClientPipe(cr, cw)
	if err != nil {
		srv.Close()
		return nil, nil, err
	}
	// the pipes first: the client's Close waits for its reader, which waits on the pipe
	return c, func() {
		cw.Close()
		sw.Close()
		cr.Close()
		sr.Close()
		c.Close()
		srv.Close()
	}, nil
}

// Seed fills dir with a small picture of an openccu-lite system for the page's development and
// the tests: a root file system that /proc/mounts calls read-only, the user file system on
// /usr/local, /etc/config as the link into it, accounts and groups, a few configuration files of
// the kinds the editor colours, a binary file, a file with Windows line ends and a byte order
// mark, a broken link, and a USB stick. Nothing in it is a real system's.
func Seed(dir string) error {
	files := map[string]string{
		"proc/mounts": "/dev/root / ext4 ro,relatime 0 0\n" +
			"proc /proc proc rw,relatime 0 0\nsysfs /sys sysfs rw,relatime 0 0\ndevtmpfs /dev devtmpfs rw 0 0\n" +
			"tmpfs /tmp tmpfs rw,nosuid,nodev 0 0\ntmpfs /var tmpfs rw,relatime 0 0\n" +
			"/dev/sda3 /usr/local ext4 rw,noatime 0 0\n/dev/sdb1 /media/usb1 vfat rw,relatime 0 0\n",
		"proc/version":                            "Linux version 6.12.0 (fake)\n",
		"etc/passwd":                              "root:x:0:0:root:/root:/bin/sh\ndaemon:x:1:1:daemon:/usr/sbin:/bin/false\nwww-data:x:33:33:www-data:/var/www:/bin/false\naddon-shell:x:30014:30014::/usr/local/addons/shell:/bin/false\nnobody:x:65534:65534:nobody:/:/bin/false\n",
		"etc/group":                               "root:x:0:\ndaemon:x:1:\nwww-data:x:33:\naddon-shell:x:30014:\nnogroup:x:65534:\n",
		"etc/hostname":                            "openccu-lite\n",
		"etc/os-release":                          "NAME=openccu-lite\nVERSION=0.0.0-fake\n",
		"etc/lighttpd.conf":                       "server.port = 80\n",
		"usr/local/etc/config/rfd.conf":           "Listen Port = 32001\nLog Destination = Syslog\n\n[Interface 0]\nType = HMIP_CCU2\n",
		"usr/local/etc/config/hm_addons.cfg":      "shell {CONFIG_NAME Shell CONFIG_URL /addons/shell/}\n",
		"usr/local/etc/config/crontab":            "# m h dom mon dow command\n0 3 * * * /bin/true\n",
		"usr/local/etc/config/settings.json":      "{\n  \"name\": \"fake\",\n  \"port\": 8183\n}\n",
		"usr/local/etc/config/InterfacesList.xml": "<?xml version=\"1.0\"?>\n<interfaces>\n  <ipc><name>HmIP-RF</name></ipc>\n</interfaces>\n",
		"usr/local/etc/config/windows.ini":        "\xef\xbb\xbf[section]\r\nkey=value\r\n",
		"usr/local/etc/config/no-newline.txt":     "no newline at the end",
		"usr/local/etc/config/firmware.bin":       "\x7fELF\x00\x01\x02\x03binary\x00\xff\xfe",
		"usr/local/etc/config/addon-policy/shell": "{\"mode\":\"confined\"}\n",
		"usr/local/addons/shell/versions":         "VERSION_ADDON=0.0.0\n",
		"usr/local/addons/shell/rc.d":             "#!/bin/sh\nexit 0\n",
		"media/usb1/notes.md":                     "# Notes\n\nA stick.\n",
		"media/usb1/space in name.txt":            "spaces\n",
		"root/.profile":                           "export PS1='# '\n",
		"tmp/scratch.txt":                         "tmp\n",
		"var/log/messages":                        "Nov 17 12:00:00 fake syslogd started\n",
	}
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return err
		}
	}
	for _, d := range []string{"sys", "dev", "usr/local/tmp", "usr/local/etc/config/empty"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return err
		}
	}
	_ = os.Chmod(filepath.Join(dir, "usr/local/addons/shell/rc.d"), 0o755)
	links := map[string]string{
		"etc/config":                      "/usr/local/etc/config",
		"usr/local/etc/config/broken":     "/usr/local/etc/config/gone",
		"usr/local/etc/config/hosts-link": "../../../../etc/hostname",
	}
	for name, target := range links {
		p := filepath.Join(dir, filepath.FromSlash(name))
		_ = os.Remove(p)
		if err := os.Symlink(target, p); err != nil {
			return err
		}
	}
	return nil
}
