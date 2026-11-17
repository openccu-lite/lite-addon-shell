package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/openccu-lite/lite-addon-shell/internal/sessions"
	"github.com/openccu-lite/lite-addon-shell/internal/sshc"
)

// The session's WebSocket: GET /api/v1/sessions/{id}/tty for a terminal, .../link for files. A
// files session's socket carries no bytes of its own - the files travel as requests - but it is
// how the tab holds the session (a tab that is gone for the grace ends it) and how it learns at
// once that the session ended, and why.
//
// The first message is the tab's: text, {"handle": "...", "cols": 80, "rows": 24}. The handle is
// what POST /sessions answered; it lives in the tab's memory, never in a URL or a cookie, so
// another page on the system's origin - which can send requests with the user's cookie - cannot
// attach to a session. Then:
//
//   - binary frames from the tab are input, binary frames to the tab are the terminal's output;
//   - text frames are control messages, JSON with "t": from the tab {"t":"resize","cols","rows"}
//     and {"t":"ping"}; to the tab {"t":"ready", session, now}, {"t":"pong"} and, before the
//     close, {"t":"end","reason"}.
//
// Close codes: 4000 the session ended (the reason is the close reason), 4001 another tab took the
// session over, 4003 refused (not the handle, not the web session that opened it, the transport).
const (
	closeEnded    websocket.StatusCode = 4000
	closeReplaced websocket.StatusCode = 4001
	closeRefused  websocket.StatusCode = 4003

	// maxFrame bounds one input frame (a paste).
	maxFrame = 1 << 20
)

type hello struct {
	Handle string `json:"handle"`
	Cols   int    `json:"cols"`
	Rows   int    `json:"rows"`
}

type control struct {
	T    string `json:"t"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

func (s *server) attach(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { s.socket(w, r, kind) }
}

func (s *server) socket(w http.ResponseWriter, r *http.Request, kind string) {
	id := r.PathValue("id")
	c := s.caller(r)
	// refused before the upgrade, so the page gets a status it can show
	if why := s.blocked(c); why != "" {
		fail(w, http.StatusForbidden, why, transportMessage(why), nil)
		return
	}
	info, ok := s.o.Sessions.Get(id)
	if !ok || info.Kind != kind {
		fail(w, http.StatusNotFound, "not-found", "no such session", nil)
		return
	}
	// only the web session that opened it: another administrator can end a session, not join it
	if info.Owner != c.who.SID {
		fail(w, http.StatusForbidden, "forbidden", "this session belongs to another sign-in", nil)
		return
	}
	if admin, _, err := s.fresh(r.Context(), c); err != nil || !admin {
		fail(w, http.StatusForbidden, "forbidden", "administrators only", nil)
		return
	}
	// Accept checks the Origin against the Host: a page of another origin cannot open this socket
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(maxFrame)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	hctx, hcancel := context.WithTimeout(ctx, 10*time.Second)
	typ, data, err := ws.Read(hctx)
	hcancel()
	var h hello
	if err != nil || typ != websocket.MessageText || json.Unmarshal(data, &h) != nil {
		ws.Close(closeRefused, "hello")
		return
	}
	att, info, err := s.o.Sessions.Attach(id, h.Handle)
	if err != nil {
		ws.Close(closeRefused, "handle")
		return
	}
	defer s.o.Sessions.Detach(id, att)
	if h.Cols > 0 && h.Rows > 0 {
		_ = s.o.Sessions.Resize(id, att, sshc.Size{Cols: h.Cols, Rows: h.Rows})
	}
	send := func(typ websocket.MessageType, b []byte) error {
		wctx, wcancel := context.WithTimeout(ctx, 30*time.Second)
		defer wcancel()
		return ws.Write(wctx, typ, b)
	}
	ready, _ := json.Marshal(map[string]any{"t": "ready", "session": sessionView{Info: info, Mine: true}, "now": time.Now().UnixMilli()})
	if send(websocket.MessageText, ready) != nil {
		return
	}
	if len(att.Replay) > 0 && send(websocket.MessageBinary, att.Replay) != nil {
		return
	}

	// the tab's frames: input and control
	readErr := make(chan error, 1)
	pong := make(chan struct{}, 1)
	go func() {
		for {
			typ, data, err := ws.Read(ctx)
			if err != nil {
				readErr <- err
				return
			}
			if typ == websocket.MessageBinary {
				if err := s.o.Sessions.Input(id, att, data); err != nil {
					readErr <- err
					return
				}
				continue
			}
			var m control
			if json.Unmarshal(data, &m) != nil {
				continue
			}
			switch m.T {
			case "resize":
				_ = s.o.Sessions.Resize(id, att, sshc.Size{Cols: m.Cols, Rows: m.Rows})
			case "ping":
				select {
				case pong <- struct{}{}:
				default:
				}
			}
		}
	}()

	// a ping keeps the proxied connection from idling out and notices a tab that is gone
	keep := time.NewTicker(20 * time.Second)
	defer keep.Stop()
	for {
		select {
		case data := <-att.Out:
			if send(websocket.MessageBinary, data) != nil {
				return
			}
		case <-pong:
			if send(websocket.MessageText, []byte(`{"t":"pong"}`)) != nil {
				return
			}
		case <-keep.C:
			pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
			err := ws.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		case <-att.Gone:
			reason := att.Reason()
			if reason == "" {
				ws.Close(closeReplaced, "replaced")
				return
			}
			end, _ := json.Marshal(map[string]any{"t": "end", "reason": reason})
			_ = send(websocket.MessageText, end)
			ws.Close(closeEnded, reason)
			return
		case err := <-readErr:
			if errors.Is(err, sessions.ErrNoHandle) {
				continue // the session ended or was taken over: Gone says which
			}
			return
		}
	}
}
