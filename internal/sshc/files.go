package sshc

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// ErrNoSFTP says the SSH server offers no SFTP subsystem: there is no file access without it.
var ErrNoSFTP = errors.New("the SSH server offers no SFTP")

// Files is an open root session for files: one SSH connection with the server's SFTP subsystem on
// it. Every path the addon touches is resolved there, by the system's own SFTP server, as root -
// the addon's process opens no file of the system itself.
type Files struct {
	// SFTP is the client; it may be used from several requests at once.
	SFTP   *sftp.Client
	client *ssh.Client

	once sync.Once
	done chan struct{}
}

// OpenFiles signs in with the password and starts the SFTP subsystem. The errors are Open's, and
// ErrNoSFTP.
func (c *Client) OpenFiles(ctx context.Context, password string) (*Files, error) {
	client, err := c.login(ctx, password)
	if err != nil {
		return nil, err
	}
	return files(client)
}

// OpenFilesWithKey is OpenFiles with a key instead of root's password.
func (c *Client) OpenFilesWithKey(ctx context.Context, key ssh.Signer) (*Files, error) {
	client, err := c.loginKey(ctx, key)
	if err != nil {
		return nil, err
	}
	return files(client)
}

// files starts the SFTP subsystem on the connection, which becomes the session's own.
func files(client *ssh.Client) (*Files, error) {
	sc, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("%w: %v", ErrNoSFTP, err)
	}
	f := &Files{SFTP: sc, client: client, done: make(chan struct{})}
	go func() {
		_ = client.Wait()
		close(f.done)
	}()
	go keepalive(client, f.done, f.Close)
	return f, nil
}

// Done is closed when the connection has ended.
func (f *Files) Done() <-chan struct{} { return f.done }

// Clean is false: a files session does not end by itself, only with its connection.
func (f *Files) Clean() bool { return false }

// Close ends the session and its connection.
func (f *Files) Close() {
	f.once.Do(func() {
		_ = f.SFTP.Close()
		_ = f.client.Close()
	})
}
