// Package fakesshd is an SSH server for the tests and the page's development: it speaks the real
// protocol (golang.org/x/crypto/ssh), takes one account's password or refuses passwords
// altogether ("keys only"), and runs a small shell of its own on the pty - a prompt, an echo, and
// a handful of commands (id, whoami, echo, size, flood, sleep, exit). No process is started and
// no file is touched. The end-to-end test in test/sshd runs against a real OpenSSH sshd instead.
package fakesshd

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Server is the fake sshd.
type Server struct {
	// User and Password are the one account ("root" by default).
	User, Password string

	mu       sync.Mutex
	keysOnly bool
	l        net.Listener
	signer   ssh.Signer
	conns    map[net.Conn]bool
	logins   int
}

// New makes a server with a fresh ed25519 host key, not listening yet.
func New(password string) (*Server, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, err
	}
	return &Server{User: "root", Password: password, signer: signer, conns: map[net.Conn]bool{}}, nil
}

// HostKey is the public host key in authorized_keys form (what /etc/ssh/ssh_host_*_key.pub holds).
func (s *Server) HostKey() []byte { return ssh.MarshalAuthorizedKey(s.signer.PublicKey()) }

// WriteHostKey writes the public host key to a file for the client to pin.
func (s *Server) WriteHostKey(path string) error { return os.WriteFile(path, s.HostKey(), 0o644) }

// SetKeysOnly makes the server refuse passwords (PasswordAuthentication no).
func (s *Server) SetKeysOnly(on bool) {
	s.mu.Lock()
	s.keysOnly = on
	s.mu.Unlock()
}

// Logins counts the accepted logins.
func (s *Server) Logins() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins
}

// Listen starts the server on addr ("127.0.0.1:0" for a free port).
func (s *Server) Listen(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.l = l
	s.mu.Unlock()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns[c] = true
			s.mu.Unlock()
			go s.serve(c)
		}
	}()
	return nil
}

// Addr is the listening address.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.l == nil {
		return ""
	}
	return s.l.Addr().String()
}

// Stop closes the listener; open connections stay (as sshd's children do when only the listener
// goes).
func (s *Server) Stop() {
	s.mu.Lock()
	l := s.l
	s.l = nil
	s.mu.Unlock()
	if l != nil {
		l.Close()
	}
}

// Kill closes the listener and every connection (sshd going away mid-session).
func (s *Server) Kill() {
	s.Stop()
	s.mu.Lock()
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
}

func (s *Server) config() *ssh.ServerConfig {
	cfg := &ssh.ServerConfig{}
	s.mu.Lock()
	keysOnly := s.keysOnly
	s.mu.Unlock()
	if !keysOnly {
		cfg.PasswordCallback = func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if c.User() == s.User && string(pw) == s.Password {
				return nil, nil
			}
			return nil, errors.New("wrong password")
		}
	}
	// a key that never matches, so "publickey" is offered as on a real sshd
	cfg.PublicKeyCallback = func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
		return nil, errors.New("no key is authorised")
	}
	cfg.AddHostKey(s.signer)
	return cfg
}

func (s *Server) serve(c net.Conn) {
	defer func() {
		c.Close()
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
	}()
	conn, chans, reqs, err := ssh.NewServerConn(c, s.config())
	if err != nil {
		return
	}
	s.mu.Lock()
	s.logins++
	s.mu.Unlock()
	defer conn.Close()
	go func() {
		for r := range reqs {
			if r.WantReply {
				_ = r.Reply(r.Type == "keepalive@openssh.com", nil)
			}
		}
	}()
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "sessions only")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			return
		}
		go session(ch, creqs)
	}
}

type winsize struct {
	mu         sync.Mutex
	cols, rows uint32
}

func session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	ws := &winsize{cols: 80, rows: 24}
	started := make(chan bool, 1)
	go func() {
		for r := range reqs {
			ok := false
			switch r.Type {
			case "pty-req":
				// string TERM, uint32 cols, uint32 rows, ...
				if len(r.Payload) >= 4 {
					n := binary.BigEndian.Uint32(r.Payload)
					if rest := r.Payload[4:]; uint32(len(rest)) >= n+8 {
						ws.mu.Lock()
						ws.cols = binary.BigEndian.Uint32(rest[n:])
						ws.rows = binary.BigEndian.Uint32(rest[n+4:])
						ws.mu.Unlock()
						ok = true
					}
				}
			case "window-change":
				if len(r.Payload) >= 8 {
					ws.mu.Lock()
					ws.cols = binary.BigEndian.Uint32(r.Payload)
					ws.rows = binary.BigEndian.Uint32(r.Payload[4:])
					ws.mu.Unlock()
					ok = true
				}
			case "shell":
				ok = true
				select {
				case started <- true:
				default:
				}
			case "env":
				ok = true
			}
			if r.WantReply {
				_ = r.Reply(ok, nil)
			}
		}
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		return
	}
	status := shell(ch, ws)
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
}

const prompt = "# "

// shell is the small shell: it echoes what is typed (a pty in cooked mode would), runs a line at
// Enter, and answers the exit status.
func shell(ch ssh.Channel, ws *winsize) uint32 {
	out := func(s string) { _, _ = ch.Write([]byte(strings.ReplaceAll(s, "\n", "\r\n"))) }
	out("fake shell - not a real system\n" + prompt)
	var line []byte
	buf := make([]byte, 4096)
	for {
		n, err := ch.Read(buf)
		if err != nil {
			return 0
		}
		for _, b := range buf[:n] {
			switch b {
			case '\r', '\n':
				out("\n")
				cmd := strings.TrimSpace(string(line))
				line = line[:0]
				if code, exit := run(cmd, out, ws); exit {
					return code
				}
				out(prompt)
			case 0x7f, 0x08:
				if len(line) > 0 {
					line = line[:len(line)-1]
					out("\b \b")
				}
			case 0x03: // Ctrl+C
				line = line[:0]
				out("^C\n" + prompt)
			case 0x04: // Ctrl+D
				if len(line) == 0 {
					out("logout\n")
					return 0
				}
			default:
				if b >= 0x20 {
					line = append(line, b)
					_, _ = ch.Write([]byte{b})
				}
			}
		}
	}
}

func run(cmd string, out func(string), ws *winsize) (uint32, bool) {
	name, arg, _ := strings.Cut(cmd, " ")
	switch name {
	case "":
	case "id":
		out("uid=0(root) gid=0(root) groups=0(root)\n")
	case "whoami":
		out("root\n")
	case "echo":
		out(arg + "\n")
	case "size":
		ws.mu.Lock()
		out(fmt.Sprintf("%d %d\n", ws.cols, ws.rows))
		ws.mu.Unlock()
	case "flood":
		// flood N: N lines of 100 characters
		n, _ := strconv.Atoi(arg)
		row := strings.Repeat("0123456789", 10) + "\n"
		for i := 0; i < n; i++ {
			out(row)
		}
		out("flood done\n")
	case "sleep":
		n, _ := strconv.Atoi(arg)
		time.Sleep(time.Duration(n) * time.Second)
	case "exit", "logout":
		code, _ := strconv.Atoi(arg)
		out("logout\n")
		return uint32(code), true
	default:
		out("sh: " + name + ": not found\n")
	}
	return 0, false
}
