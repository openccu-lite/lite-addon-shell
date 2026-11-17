package sshc_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openccu-lite/lite-addon-shell/internal/fakesshd"
	"github.com/openccu-lite/lite-addon-shell/internal/sshc"
)

func start(t *testing.T) (*fakesshd.Server, *sshc.Client) {
	t.Helper()
	srv, err := fakesshd.New("root-pass")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Kill)
	key := filepath.Join(t.TempDir(), "ssh_host_ed25519_key.pub")
	if err := srv.WriteHostKey(key); err != nil {
		t.Fatal(err)
	}
	return srv, &sshc.Client{Addr: srv.Addr(), User: "root", HostKeyGlobs: []string{filepath.Join(filepath.Dir(key), "ssh_host_*_key.pub")}, Timeout: 5 * time.Second}
}

// readUntil reads the shell's output until it holds want.
func readUntil(t *testing.T, sh *sshc.Shell, want string) string {
	t.Helper()
	var got bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for !strings.Contains(got.String(), want) {
			n, err := sh.Read(buf)
			got.Write(buf[:n])
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("no %q in %q", want, got.String())
	}
	if !strings.Contains(got.String(), want) {
		t.Fatalf("no %q in %q", want, got.String())
	}
	return got.String()
}

// The probe says what sshd offers without trying a password; nothing is logged in.
func TestProbe(t *testing.T) {
	srv, c := start(t)
	ctx := t.Context()
	if st := c.Probe(ctx); st != sshc.StateOn {
		t.Fatalf("a sshd that takes passwords: %s", st)
	}
	srv.SetKeysOnly(true)
	if st := c.Probe(ctx); st != sshc.StateKeysOnly {
		t.Fatalf("a sshd that refuses passwords: %s", st)
	}
	if srv.Logins() != 0 {
		t.Fatalf("the probe logged in %d times", srv.Logins())
	}
	// another host key than the pinned one: neither on nor off
	other, _ := fakesshd.New("x")
	if err := other.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer other.Kill()
	c2 := *c
	c2.Addr = other.Addr()
	if st := c2.Probe(ctx); st != sshc.StateUnknown {
		t.Fatalf("a foreign host key: %s", st)
	}
	srv.Stop()
	if st := c.Probe(ctx); st != sshc.StateOff {
		t.Fatalf("no listener: %s", st)
	}
}

// A sign-in with root's password opens a shell on a pty of the asked size; a resize reaches it;
// exit ends it cleanly.
func TestOpenShell(t *testing.T) {
	srv, c := start(t)
	sh, err := c.Open(t.Context(), "root-pass", sshc.Size{Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	readUntil(t, sh, "# ")
	if _, err := sh.Write([]byte("id\r")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, sh, "uid=0(root)")
	_, _ = sh.Write([]byte("size\r"))
	readUntil(t, sh, "100 30")
	if err := sh.Resize(sshc.Size{Cols: 132, Rows: 43}); err != nil {
		t.Fatal(err)
	}
	// the fake handles the request beside the input: ask until it has arrived
	_, _ = sh.Write([]byte("sleep 1\rsize\r"))
	readUntil(t, sh, "132 43")
	_, _ = sh.Write([]byte("exit 3\r"))
	select {
	case <-sh.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the shell did not end")
	}
	if !sh.Clean() {
		t.Error("an exit with a status is the shell's own end")
	}
	if srv.Logins() != 1 {
		t.Errorf("logins %d", srv.Logins())
	}
}

// The sign-in's errors: a wrong password, a sshd that takes keys only, no sshd, a foreign host
// key - which is refused before any password is sent.
func TestOpenErrors(t *testing.T) {
	srv, c := start(t)
	ctx := t.Context()
	if _, err := c.Open(ctx, "wrong", sshc.Size{}); !errors.Is(err, sshc.ErrWrongPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	srv.SetKeysOnly(true)
	if _, err := c.Open(ctx, "root-pass", sshc.Size{}); !errors.Is(err, sshc.ErrKeysOnly) {
		t.Fatalf("keys only: %v", err)
	}
	srv.SetKeysOnly(false)

	other, _ := fakesshd.New("root-pass")
	if err := other.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer other.Kill()
	c2 := *c
	c2.Addr = other.Addr()
	if _, err := c2.Open(ctx, "root-pass", sshc.Size{}); !errors.Is(err, sshc.ErrHostKey) {
		t.Fatalf("a foreign host key: %v", err)
	}
	if other.Logins() != 0 {
		t.Fatal("the password went to a server with a foreign host key")
	}
	// no readable host key file: refused, whatever the server
	c3 := *c
	c3.HostKeyGlobs = []string{filepath.Join(t.TempDir(), "none*.pub")}
	if _, err := c3.Open(ctx, "root-pass", sshc.Size{}); !errors.Is(err, sshc.ErrHostKey) {
		t.Fatalf("no host key file: %v", err)
	}
	srv.Stop()
	if _, err := c.Open(ctx, "root-pass", sshc.Size{}); !errors.Is(err, sshc.ErrOff) {
		t.Fatalf("no listener: %v", err)
	}
}

// sshd going away mid-session ends the shell, and not as the shell's own exit.
func TestConnectionLost(t *testing.T) {
	srv, c := start(t)
	sh, err := c.Open(context.Background(), "root-pass", sshc.Size{})
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	readUntil(t, sh, "# ")
	srv.Kill()
	select {
	case <-sh.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the shell did not notice")
	}
	if sh.Clean() {
		t.Error("a lost connection is not the shell's own end")
	}
}
