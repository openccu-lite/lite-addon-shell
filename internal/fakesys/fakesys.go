// Package fakesys is the slice of openccu-lite the addon talks to, for the tests and the page's
// development: GET /api/auth/v1/state for a web session (with ?peek=1, and counting which asks
// touched the session), and the addon notices of the Status page. /dev routes let a test end a
// web session, switch the login off and read the notices.
package fakesys

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

// Account is a web session the fake knows.
type Account struct {
	User, Level string
}

// Server is the fake system.
type Server struct {
	mu sync.Mutex
	// Sessions are the live web sessions by id.
	Sessions map[string]Account
	// AuthOff answers every caller as the anonymous administrator.
	AuthOff bool
	// Old makes the fake a system from before ?peek=1: the parameter is ignored and the ask touches.
	Old bool
	// Down answers 503 (occulited restarting).
	Down bool
	// ControlToken is the addon's control token for the notices; "" accepts any bearer.
	ControlToken string
	// Notices are the posted notices by id, as posted.
	Notices map[string]map[string]any
	// Touched counts the asks that refreshed a session's idle timer, Peeked those that did not.
	Touched, Peeked map[string]int
}

// New makes a fake with one administrator's session.
func New(sid, user string) *Server {
	return &Server{Sessions: map[string]Account{sid: {User: user, Level: "administer"}}, Notices: map[string]map[string]any{},
		Touched: map[string]int{}, Peeked: map[string]int{}}
}

// With runs f under the fake's lock (a test changing or reading its state).
func (s *Server) With(f func(*Server)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

// Handler is the fake's routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/auth/v1/state", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.Down {
			writeJSON(w, 503, map[string]any{"error": "starting"})
			return
		}
		sid := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		peek := r.URL.Query().Get("peek") != "" && !s.Old
		out := map[string]any{"setup_required": false, "authenticated": false}
		if peek {
			out["peek"] = true
		}
		if s.AuthOff {
			out["authenticated"], out["user"], out["level"], out["role"], out["auth_off"], out["sid"] = true, "admin", "administer", "admin", true, sid
			writeJSON(w, 200, out)
			return
		}
		if a, ok := s.Sessions[sid]; ok {
			if peek {
				s.Peeked[sid]++
			} else {
				s.Touched[sid]++
			}
			out["authenticated"], out["user"], out["level"], out["sid"], out["method"] = true, a.User, a.Level, sid, "password"
			out["role"] = map[bool]string{true: "admin", false: "user"}[a.Level == "administer"]
		}
		writeJSON(w, 200, out)
	})
	notice := func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.ControlToken != "" && strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") != s.ControlToken {
			writeJSON(w, 401, map[string]any{"error": "unauthenticated"})
			return
		}
		id := r.PathValue("id")
		if r.Method == http.MethodDelete {
			if _, ok := s.Notices[id]; !ok {
				writeJSON(w, 404, map[string]any{"error": "not_found"})
				return
			}
			delete(s.Notices, id)
			writeJSON(w, 200, map[string]any{"ok": true})
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			writeJSON(w, 400, map[string]any{"error": "bad-request"})
			return
		}
		_, had := s.Notices[id]
		s.Notices[id] = body
		writeJSON(w, map[bool]int{true: 200, false: 201}[had], body)
	}
	mux.HandleFunc("PUT /api/system/v1/addon-notices/{id}", notice)
	mux.HandleFunc("DELETE /api/system/v1/addon-notices/{id}", notice)

	// the dev routes: what a test does to the system
	mux.HandleFunc("GET /dev/notices", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, 200, s.Notices)
	})
	mux.HandleFunc("GET /dev/asks", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"touched": s.Touched, "peeked": s.Peeked})
	})
	// /dev/session?sid=…&user=…&level=…: makes or changes a web session; level=none ends it
	mux.HandleFunc("POST /dev/session", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		q := r.URL.Query()
		if q.Get("level") == "none" {
			delete(s.Sessions, q.Get("sid"))
		} else {
			s.Sessions[q.Get("sid")] = Account{User: q.Get("user"), Level: q.Get("level")}
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /dev/auth-off", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.AuthOff = r.URL.Query().Get("on") == "1"
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
