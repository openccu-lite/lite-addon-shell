// Package sshc is the addon's SSH client to the system's own sshd on the loopback: it asks what
// sshd offers (is it running, does it take a password), and opens a root session with a pty. The
// host key is pinned to the system's own host key files, so nothing else on the loopback can pass
// for sshd. Root's password is handed to the SSH library for the one login and kept nowhere.
package sshc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// What sshd offers.
const (
	StateOff      = "off"       // nothing listens: SSH is switched off
	StateOn       = "on"        // sshd takes a password
	StateKeysOnly = "keys-only" // sshd runs and refuses passwords ("Only key login")
	StateUnknown  = "unknown"   // sshd answered something else (its host key is not the system's)
)

// Errors of a login.
var (
	ErrOff           = errors.New("SSH is switched off")
	ErrKeysOnly      = errors.New("SSH accepts keys only")
	ErrWrongPassword = errors.New("wrong password")
	ErrHostKey       = errors.New("the SSH server's host key is not the system's")
	// ErrKeyRefused is a key the server does not take: no line for it in root's authorized keys,
	// or a line whose time is up or that is for another address.
	ErrKeyRefused = errors.New("the SSH server refused the key")
)

// Client reaches one sshd.
type Client struct {
	// Addr is sshd's address, 127.0.0.1:22.
	Addr string
	// User is the account, root.
	User string
	// HostKeyGlobs are the public host key files the server's key must be one of
	// (/etc/ssh/ssh_host_*_key.pub).
	HostKeyGlobs []string
	// Timeout bounds the connect and the handshake; 10 s by default.
	Timeout time.Duration
	// ProcFiles are the kernel's tables of TCP sockets, /proc/net/tcp and /proc/net/tcp6 by
	// default (a test names its own).
	ProcFiles []string
}

func (c *Client) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 10 * time.Second
}

// hostKeys reads the pinned keys; an error when none is readable.
func (c *Client) hostKeys() ([]ssh.PublicKey, error) {
	var keys []ssh.PublicKey
	for _, g := range c.HostKeyGlobs {
		files, _ := filepath.Glob(g)
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			k, _, _, _, err := ssh.ParseAuthorizedKey(b)
			if err != nil {
				continue
			}
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: no host key file is readable (%s)", ErrHostKey, strings.Join(c.HostKeyGlobs, ", "))
	}
	return keys, nil
}

func (c *Client) config(auth []ssh.AuthMethod) (*ssh.ClientConfig, error) {
	keys, err := c.hostKeys()
	if err != nil {
		return nil, err
	}
	var algos []string
	seen := map[string]bool{}
	for _, k := range keys {
		for _, a := range algorithmsOf(k.Type()) {
			if !seen[a] {
				seen[a] = true
				algos = append(algos, a)
			}
		}
	}
	return &ssh.ClientConfig{
		User: c.User,
		Auth: auth,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			m := key.Marshal()
			for _, k := range keys {
				if bytes.Equal(k.Marshal(), m) {
					return nil
				}
			}
			return ErrHostKey
		},
		// only the algorithms of the keys we know, so the server presents one of them
		HostKeyAlgorithms: algos,
		Timeout:           c.timeout(),
		ClientVersion:     "SSH-2.0-lite-addon-shell",
	}, nil
}

// algorithmsOf lists the signature algorithms for a host key type (an RSA key signs with SHA-2).
func algorithmsOf(keyType string) []string {
	if keyType == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
	}
	return []string{keyType}
}

// Listening says whether something listens on sshd's port, read from the kernel's socket tables
// without connecting - sshd writes a log line for every connection, and the watch asks every 15
// seconds. known is false where the tables cannot be read; the caller then probes.
func (c *Client) Listening() (listening, known bool) {
	_, port, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return false, false
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return false, false
	}
	want := fmt.Sprintf(":%04X", n)
	files := c.ProcFiles
	if len(files) == 0 {
		files = []string{"/proc/net/tcp", "/proc/net/tcp6"}
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		known = true
		for _, line := range strings.Split(string(b), "\n") {
			// sl local_address rem_address st ...; st 0A is LISTEN
			fields := strings.Fields(line)
			if len(fields) > 3 && fields[3] == "0A" && strings.HasSuffix(strings.ToUpper(fields[1]), want) {
				return true, true
			}
		}
	}
	return false, known
}

var errProbe = errors.New("probe")

// Probe asks sshd what it offers without trying a password: it connects, lets the server list its
// methods for the account, and leaves. sshd logs one closed pre-authentication connection.
func (c *Client) Probe(ctx context.Context) string {
	asked := false
	cfg, err := c.config([]ssh.AuthMethod{ssh.PasswordCallback(func() (string, error) {
		asked = true
		return "", errProbe
	})})
	if err != nil {
		// without a readable host key the state is still "off" when nothing listens
		if conn, derr := c.dial(ctx); derr != nil {
			return StateOff
		} else {
			conn.Close()
		}
		return StateUnknown
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return StateOff
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(c.timeout()))
	cc, _, _, err := ssh.NewClientConn(conn, c.Addr, cfg)
	if err == nil {
		// a server that lets root in without any credential: not a state the addon works with
		cc.Close()
		return StateUnknown
	}
	if asked {
		return StateOn
	}
	if errors.Is(err, ErrHostKey) || strings.Contains(err.Error(), ErrHostKey.Error()) {
		return StateUnknown
	}
	if strings.Contains(err.Error(), "unable to authenticate") {
		return StateKeysOnly
	}
	return StateUnknown
}

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: c.timeout()}
	return d.DialContext(ctx, "tcp", c.Addr)
}

// Shell is an open root session with a pty.
type Shell struct {
	client *ssh.Client
	sess   *ssh.Session
	stdin  io.WriteCloser
	out    io.Reader

	once sync.Once
	done chan struct{}
	err  error
}

// Size is a terminal size; out-of-range values are clamped.
type Size struct{ Cols, Rows int }

func (s Size) clamp() Size {
	c := func(v, lo, hi, def int) int {
		if v == 0 {
			return def
		}
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}
	return Size{Cols: c(s.Cols, 10, 500, 80), Rows: c(s.Rows, 2, 300, 24)}
}

// login signs in with the password and answers the connection. The errors are ErrOff, ErrKeysOnly,
// ErrWrongPassword, ErrHostKey, or what the connection said.
func (c *Client) login(ctx context.Context, password string) (*ssh.Client, error) {
	offered := false
	return c.connect(ctx, ssh.PasswordCallback(func() (string, error) {
		offered = true
		return password, nil
	}), func() error {
		if !offered {
			return ErrKeysOnly
		}
		return ErrWrongPassword
	})
}

// loginKey signs in with a key instead of a password. sshd is the one that decides whether the
// key counts right now: the line in root's authorized keys carries the address it is for and the
// time until which it is good. The errors are ErrOff, ErrKeyRefused, ErrHostKey, or what the
// connection said.
func (c *Client) loginKey(ctx context.Context, key ssh.Signer) (*ssh.Client, error) {
	return c.connect(ctx, ssh.PublicKeys(key), func() error { return ErrKeyRefused })
}

// connect dials sshd and authenticates with the one method; refused names the error for a server
// that did not take it.
func (c *Client) connect(ctx context.Context, auth ssh.AuthMethod, refused func() error) (*ssh.Client, error) {
	cfg, err := c.config([]ssh.AuthMethod{auth})
	if err != nil {
		return nil, err
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, ErrOff
	}
	_ = conn.SetDeadline(time.Now().Add(c.timeout()))
	cc, chans, reqs, err := ssh.NewClientConn(conn, c.Addr, cfg)
	if err != nil {
		conn.Close()
		switch {
		case errors.Is(err, ErrHostKey) || strings.Contains(err.Error(), ErrHostKey.Error()):
			return nil, ErrHostKey
		case strings.Contains(err.Error(), "unable to authenticate"):
			return nil, refused()
		}
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(cc, chans, reqs), nil
}

// keepalive notices an sshd that went away: the connection is one session's alone. It runs until
// done is closed and calls lost when a request is not answered.
func keepalive(client *ssh.Client, done <-chan struct{}, lost func()) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				lost()
				return
			}
		}
	}
}

// Open signs in with the password and starts the account's shell on a pty. The errors are ErrOff,
// ErrKeysOnly, ErrWrongPassword, ErrHostKey, or what the connection said.
func (c *Client) Open(ctx context.Context, password string, size Size) (*Shell, error) {
	client, err := c.login(ctx, password)
	if err != nil {
		return nil, err
	}
	return shell(client, size)
}

// OpenWithKey is Open with a key instead of root's password (loginKey says what decides).
func (c *Client) OpenWithKey(ctx context.Context, key ssh.Signer, size Size) (*Shell, error) {
	client, err := c.loginKey(ctx, key)
	if err != nil {
		return nil, err
	}
	return shell(client, size)
}

// shell starts the account's shell on a pty of the connection, which becomes the shell's own.
func shell(client *ssh.Client, size Size) (*Shell, error) {
	sess, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, err
	}
	size = size.clamp()
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 38400, ssh.TTY_OP_OSPEED: 38400}
	if err := sess.RequestPty("xterm-256color", size.Rows, size.Cols, modes); err != nil {
		client.Close()
		return nil, fmt.Errorf("the pty was refused: %w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		client.Close()
		return nil, err
	}
	out, err := sess.StdoutPipe()
	if err != nil {
		client.Close()
		return nil, err
	}
	if err := sess.Shell(); err != nil {
		client.Close()
		return nil, err
	}
	sh := &Shell{client: client, sess: sess, stdin: stdin, out: out, done: make(chan struct{})}
	go func() {
		sh.err = sess.Wait()
		close(sh.done)
	}()
	go keepalive(client, sh.done, sh.Close)
	return sh, nil
}

// Read reads the terminal's output (stdout and stderr are one on a pty).
func (s *Shell) Read(p []byte) (int, error) { return s.out.Read(p) }

// Clean reports, once Done is closed, whether the shell ended by itself (an exit, whatever its
// status) rather than with its connection.
func (s *Shell) Clean() bool {
	if s.err == nil {
		return true
	}
	var ee *ssh.ExitError
	return errors.As(s.err, &ee)
}

// Write sends input to the shell.
func (s *Shell) Write(p []byte) (int, error) { return s.stdin.Write(p) }

// Resize tells the pty its new size.
func (s *Shell) Resize(size Size) error {
	size = size.clamp()
	return s.sess.WindowChange(size.Rows, size.Cols)
}

// Done is closed when the shell has ended (the user's exit, or the connection).
func (s *Shell) Done() <-chan struct{} { return s.done }

// Close ends the session and its connection.
func (s *Shell) Close() {
	s.once.Do(func() {
		_ = s.sess.Close()
		_ = s.client.Close()
	})
}
