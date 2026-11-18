// fakesystem runs the fake openccu-lite and a fake sshd for the page's development and the
// Playwright tests (scripts/dev-serve.sh): the system on -addr (the web session of the daemon's
// development mode is an administrator's), sshd on -sshd with root's password -password and its
// public host key written to -host-key. /dev/sshd?mode=on|keys-only|refusing|off switches the fake sshd,
// /dev/session ends or changes the web session; see internal/fakesys. With -files the fake sshd
// offers SFTP over that directory, seeded like a small system (fakesshd.Seed); /dev/fs/reset seeds
// it anew, /dev/fs/write?path= and /dev/fs/read?path= change and read a file behind the page's
// back, /dev/fs/fail?op=PosixRename|Write|... makes that operation fail until it is called
// without op (a bind-mounted file, a full disk).
package main

import (
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/openccu-lite/lite-addon-shell/internal/fakesshd"
	"github.com/openccu-lite/lite-addon-shell/internal/fakesys"
	"github.com/openccu-lite/lite-addon-shell/internal/httpapi"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18080", "the fake system's address")
	sshAddr := flag.String("sshd", "127.0.0.1:18022", "the fake sshd's address")
	password := flag.String("password", "root-pass", "root's password at the fake sshd")
	hostKey := flag.String("host-key", "", "where to write the fake sshd's public host key")
	filesDir := flag.String("files", "", "a scratch directory the fake sshd serves over SFTP as the system's files (emptied and seeded)")
	flag.Parse()
	sshd, err := fakesshd.New(*password)
	if err != nil {
		log.Fatal(err)
	}
	reseed := func() error {
		// only ever a directory this program was told to fill
		entries, _ := os.ReadDir(*filesDir)
		for _, e := range entries {
			if err := os.RemoveAll(filepath.Join(*filesDir, e.Name())); err != nil {
				return err
			}
		}
		return fakesshd.Seed(*filesDir)
	}
	var failMu sync.Mutex
	failOp, failPath := "", ""
	if *filesDir != "" {
		if err := os.MkdirAll(*filesDir, 0o755); err != nil {
			log.Fatal(err)
		}
		if err := reseed(); err != nil {
			log.Fatal(err)
		}
		sshd.Files = fakesshd.NewTree(*filesDir)
		sshd.Files.Fail = func(op, p string) error {
			failMu.Lock()
			defer failMu.Unlock()
			if failOp != "" && op == failOp && strings.Contains(p, failPath) {
				if op == "Write" {
					return syscall.ENOSPC
				}
				return syscall.EBUSY
			}
			return nil
		}
	}
	if *hostKey != "" {
		if err := sshd.WriteHostKey(*hostKey); err != nil {
			log.Fatal(err)
		}
	}
	if err := sshd.Listen(*sshAddr); err != nil {
		log.Fatal(err)
	}
	sys := fakesys.New(httpapi.DevSID, "dev")
	mux := http.NewServeMux()
	mux.Handle("/", sys.Handler())
	var mu sync.Mutex
	mux.HandleFunc("POST /dev/sshd", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		sshd.SetRefusing(false)
		switch r.URL.Query().Get("mode") {
		case "refusing":
			sshd.SetRefusing(true)
		case "off":
			sshd.Stop()
		case "kill":
			sshd.Kill()
		case "keys-only":
			sshd.SetKeysOnly(true)
			if sshd.Addr() == "" {
				_ = sshd.Listen(*sshAddr)
			}
		default:
			sshd.SetKeysOnly(false)
			if sshd.Addr() == "" {
				_ = sshd.Listen(*sshAddr)
			}
		}
		w.WriteHeader(204)
	})
	inTree := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		if *filesDir == "" {
			http.Error(w, "no -files", http.StatusNotFound)
			return "", false
		}
		return filepath.Join(*filesDir, filepath.FromSlash(path.Clean("/"+r.URL.Query().Get("path")))), true
	}
	mux.HandleFunc("POST /dev/fs/reset", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := inTree(w, r); !ok {
			return
		}
		failMu.Lock()
		failOp, failPath = "", ""
		sshd.Files.NoSpace = false
		failMu.Unlock()
		if err := reseed(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /dev/fs/write", func(w http.ResponseWriter, r *http.Request) {
		p, ok := inTree(w, r)
		if !ok {
			return
		}
		data, _ := io.ReadAll(io.LimitReader(r.Body, 32<<20))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /dev/fs/read", func(w http.ResponseWriter, r *http.Request) {
		p, ok := inTree(w, r)
		if !ok {
			return
		}
		data, err := os.ReadFile(p)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		fi, _ := os.Stat(p)
		w.Header().Set("X-Mode", fi.Mode().Perm().String())
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /dev/fs/list", func(w http.ResponseWriter, r *http.Request) {
		p, ok := inTree(w, r)
		if !ok {
			return
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		for _, e := range entries {
			_, _ = io.WriteString(w, e.Name()+"\n")
		}
	})
	mux.HandleFunc("POST /dev/fs/fail", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := inTree(w, r); !ok {
			return
		}
		failMu.Lock()
		failOp, failPath = r.URL.Query().Get("op"), r.URL.Query().Get("in")
		sshd.Files.NoSpace = failOp == "Write"
		failMu.Unlock()
		w.WriteHeader(204)
	})
	log.Printf("fake system on %s, fake sshd on %s", *addr, *sshAddr)
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
