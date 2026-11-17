// lite-shell is the lite-addon-shell daemon: a root shell for an openccu-lite system in the browser.
// It runs confined (the addon's own user, no capability, no API scope, no port beyond the
// loopback) and is never root itself: a session is an SSH connection to the system's own sshd on
// the loopback, signed in with root's password, which the user types for each session and which
// is kept nowhere. It serves its page and API on the loopback behind lighttpd's proxy of
// /addons/shell/. openccu-lite only.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/hobbyquaker/ccu-addon-kit/go/addonkit"

	"github.com/openccu-lite/lite-addon-shell/internal/audit"
	"github.com/openccu-lite/lite-addon-shell/internal/config"
	"github.com/openccu-lite/lite-addon-shell/internal/core"
	"github.com/openccu-lite/lite-addon-shell/internal/httpapi"
	"github.com/openccu-lite/lite-addon-shell/internal/sessions"
	"github.com/openccu-lite/lite-addon-shell/internal/sshc"
	"github.com/openccu-lite/lite-addon-shell/internal/system"
)

// version is set by the build (-ldflags "-X main.version=…").
var version = "dev"

type options struct {
	addonDir, confDir, id, base, basePath, www, sshAddr, hostKeys, noticesURL, tokenFile string
	port                                                                                 int
	noSession, insecureHTTP                                                              bool
	every, grace                                                                         time.Duration
}

func main() {
	log.SetFlags(0) // the journal adds the time
	var o options
	flag.StringVar(&o.addonDir, "addon-dir", "/usr/local/addons/shell", "the addon's directory (app/, var/)")
	flag.StringVar(&o.confDir, "conf-dir", "/usr/local/etc/config/addons/shell", "the configuration's directory (settings.json, state.json; mode 0700)")
	flag.StringVar(&o.id, "id", "shell", "the addon id (base path, notices)")
	flag.IntVar(&o.port, "port", 8290, "the loopback port lighttpd proxies to")
	flag.StringVar(&o.base, "system", "http://127.0.0.1", "openccu-lite's URL")
	flag.StringVar(&o.basePath, "base-path", "", "the path the page is served at (/addons/<id> by default)")
	flag.StringVar(&o.www, "www", "", "the built page (<addon-dir>/app by default)")
	flag.StringVar(&o.sshAddr, "ssh", "127.0.0.1:22", "the system's own sshd")
	flag.StringVar(&o.hostKeys, "host-keys", "/etc/ssh/ssh_host_*_key.pub", "the system's public host key files (comma-separated patterns): sshd must present one of them")
	flag.StringVar(&o.noticesURL, "notices", "http://127.0.0.1:8183", "the system service's loopback address for the notice on the Status page (empty: none)")
	flag.StringVar(&o.tokenFile, "control-token", "", "the addon's control token file (the system's by default)")
	flag.BoolVar(&o.noSession, "no-session-check", false, "serve without the system's session check (development only)")
	flag.BoolVar(&o.insecureHTTP, "insecure-http", false, "open root sessions over plain HTTP (development only)")
	flag.DurationVar(&o.every, "watch-every", 15*time.Second, "how often the web sessions and sshd are asked")
	flag.DurationVar(&o.grace, "grace", sessions.DefaultGrace, "how long a session waits for its tab to come back")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	if o.basePath == "" {
		o.basePath = "/addons/" + o.id
	}
	if o.www == "" {
		o.www = filepath.Join(o.addonDir, "app")
	}
	if o.tokenFile == "" {
		o.tokenFile = filepath.Join(system.ControlTokenDir, o.id)
	}
	if err := run(o); err != nil {
		log.Fatalf("shell: %v", err)
	}
}

func run(o options) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logf := func(format string, args ...any) { log.Printf("shell: "+format, args...) }
	// the audit lines stand alone in the journal, without a prefix: one event, fixed fields
	alog := &audit.Log{Sink: func(line string) { log.Print(line) }}

	store, err := config.Open(o.confDir)
	if err != nil {
		return err
	}
	ssh := &sshc.Client{Addr: o.sshAddr, User: "root", HostKeyGlobs: strings.Split(o.hostKeys, ",")}
	sys := &system.Client{Base: o.base}
	notifier := &system.Notifier{Base: o.noticesURL, TokenFile: o.tokenFile}

	var changed func()
	onChange := func() {
		if changed != nil {
			changed()
		}
	}
	mgr := sessions.New(sessions.Options{
		Open: func(ctx context.Context, password string, size sshc.Size) (sessions.Shell, error) {
			sh, err := ssh.Open(ctx, password, size)
			if err != nil {
				return nil, err
			}
			return sh, nil
		},
		Limits: func() sessions.Limits {
			s := store.Settings()
			return sessions.Limits{Idle: s.Idle(), Longest: s.Longest(), MaxSessions: s.MaxSessions}
		},
		Audit:    alog.Printf,
		OnChange: onChange,
		Grace:    o.grace,
	})
	c := core.New(core.Options{AddonID: o.id, Store: store, Sessions: mgr, Probe: ssh.Probe, System: sys, Notices: notifier,
		Audit: alog.Printf, Log: logf, OnChange: onChange, Every: o.every})

	var wrap func(http.Handler) http.Handler
	if !o.noSession {
		// administrators only, and no AddonID: the checker admits no API token, whatever its
		// scope - a root shell is for a person, not a program
		ch := addonkit.NewChecker(addonkit.SessionOptions{Base: o.base, Need: addonkit.LevelAdmin, OnRefused: func(r *http.Request, reason string) {
			logf("refused %s %s: %s", r.Method, r.URL.Path, reason)
		}})
		wrap = ch.Handler
	} else {
		logf("WARNING: running without the system's session check (development only)")
	}
	if o.insecureHTTP {
		logf("WARNING: root sessions are offered over plain HTTP (development only)")
	}
	h, ping := httpapi.New(httpapi.Options{BasePath: o.basePath, WWW: o.www, Store: store, Core: c, Sessions: mgr, System: sys, Audit: alog,
		Wrap: wrap, InsecureHTTP: o.insecureHTTP, Version: version, Started: time.Now(), Log: logf})
	changed = ping

	l, err := addonkit.Listen(o.port, "")
	if err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		mgr.Run(ctx) // ends every session when ctx ends
		close(done)
	}()
	go c.Run(ctx)
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		<-done
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
		_ = srv.Close() // the terminals' WebSockets and the event streams do not end by themselves
	}()
	sw, on := c.Switch()
	state := "switched off"
	if on {
		state = "switched on"
		if sw.Until != nil {
			state += " until " + sw.Until.Format(time.RFC3339)
		}
	}
	logf("%s: serving %s on %s; %s; sshd at %s", version, o.basePath, l.Addr(), state, o.sshAddr)
	if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
		return err
	}
	logf("stopping")
	os.Stdout.Sync()
	return nil
}
