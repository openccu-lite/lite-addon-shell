package files

import (
	"context"

	"github.com/openccu-lite/lite-addon-shell/internal/sshc"
)

// Session is a files session: its SSH connection, and the view of the system on it.
type Session struct {
	*sshc.Files
	FS *FS
}

// Open signs in to sshd with the password and opens a files session (sshc.Client.OpenFiles). log
// takes what goes wrong beside an operation.
func Open(ctx context.Context, c *sshc.Client, password string, log func(format string, args ...any)) (*Session, error) {
	conn, err := c.OpenFiles(ctx, password)
	if err != nil {
		return nil, err
	}
	fs := New(conn.SFTP)
	fs.Log = log
	return &Session{Files: conn, FS: fs}, nil
}
