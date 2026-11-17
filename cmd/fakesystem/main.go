// fakesystem runs the fake openccu-lite and a fake sshd for the page's development and the
// Playwright tests (scripts/dev-serve.sh): the system on -addr (the web session of the daemon's
// development mode is an administrator's), sshd on -sshd with root's password -password and its
// public host key written to -host-key. /dev/sshd?mode=on|keys-only|off switches the fake sshd,
// /dev/session ends or changes the web session; see internal/fakesys.
package main

import (
	"flag"
	"log"
	"net/http"
	"sync"
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
	flag.Parse()
	sshd, err := fakesshd.New(*password)
	if err != nil {
		log.Fatal(err)
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
		switch r.URL.Query().Get("mode") {
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
	log.Printf("fake system on %s, fake sshd on %s", *addr, *sshAddr)
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
