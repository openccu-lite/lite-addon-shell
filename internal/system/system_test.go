package system_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openccu-lite/lite-addon-shell/internal/fakesys"
	"github.com/openccu-lite/lite-addon-shell/internal/system"
)

const sid = "ADMINADMINADMINADMINADMIN2"

// A peek does not count as the web session's activity, the plain ask does; and the answer says
// whether the system honoured the peek (one from before the parameter does not).
func TestSessionPeek(t *testing.T) {
	f := fakesys.New(sid, "admin")
	srv := httptest.NewServer(f.Handler())
	defer srv.Close()
	c := &system.Client{Base: srv.URL + "/"}
	ctx := context.Background()
	st, err := c.Session(ctx, sid, true)
	if err != nil || !st.Peek || !st.Admin(sid) || st.User != "admin" {
		t.Fatalf("peek: %+v %v", st, err)
	}
	st, err = c.Session(ctx, sid, false)
	if err != nil || st.Peek || !st.Admin(sid) {
		t.Fatalf("plain: %+v %v", st, err)
	}
	f.With(func(s *fakesys.Server) {
		if s.Peeked[sid] != 1 || s.Touched[sid] != 1 {
			t.Errorf("peeked %d, touched %d", s.Peeked[sid], s.Touched[sid])
		}
		s.Old = true
	})
	if st, _ := c.Session(ctx, sid, true); st.Peek || !st.Admin(sid) {
		t.Fatalf("an old system: %+v", st)
	}
	// a lower level, an ended session, another session's id in the answer
	f.With(func(s *fakesys.Server) { s.Sessions[sid] = fakesys.Account{User: "conf", Level: "configure"} })
	if st, _ := c.Session(ctx, sid, true); st.Admin(sid) || !st.Authenticated {
		t.Fatalf("configure: %+v", st)
	}
	f.With(func(s *fakesys.Server) { delete(s.Sessions, sid) })
	if st, err := c.Session(ctx, sid, true); err != nil || st.Authenticated || st.Admin(sid) {
		t.Fatalf("ended: %+v %v", st, err)
	}
	if (system.State{Authenticated: true, SID: "OTHEROTHEROTHEROTHEROTHER2", Level: "administer"}).Admin(sid) {
		t.Error("another session's answer admits this one")
	}
	// the system not answering is an error, never "the session is gone"
	f.With(func(s *fakesys.Server) { s.Down = true })
	if _, err := c.Session(ctx, sid, true); err == nil {
		t.Error("a 503 is not an answer")
	}
	// nothing but a session id is ever sent: no token, no alias
	for _, bad := range []string{"", "olt_0123456789abcdef0123456789abcdef", "abcdefghij", sid + "X"} {
		if _, err := c.Session(ctx, bad, true); !errors.Is(err, system.ErrShape) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

// The Status page's notice: a warning in both languages with the way into the addon, ending by
// itself a minute after the switch's time; cleared when the switch goes off, and clearing a
// notice that is not there is no error.
func TestNotice(t *testing.T) {
	f := fakesys.New(sid, "admin")
	f.ControlToken = "ctl-token"
	srv := httptest.NewServer(f.Handler())
	defer srv.Close()
	tok := filepath.Join(t.TempDir(), "shell")
	_ = os.WriteFile(tok, []byte("ctl-token\n"), 0o600)
	n := &system.Notifier{Base: srv.URL, TokenFile: tok}
	ctx := context.Background()
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	until := now.Add(8 * time.Hour)
	if err := n.Enabled(ctx, "shell", &until, now); err != nil {
		t.Fatal(err)
	}
	f.With(func(s *fakesys.Server) {
		got := s.Notices[system.NoticeID]
		if got == nil || got["severity"] != "warning" || got["link"] != "/addon-settings/shell" || got["ttl"] != float64(8*3600+60) {
			t.Errorf("notice %v", got)
		}
		for _, k := range []string{"title", "text"} {
			m, _ := got[k].(map[string]any)
			if m["de"] == "" || m["en"] == "" || m["de"] == nil || m["en"] == nil {
				t.Errorf("%s: %v", k, m)
			}
		}
	})
	// until switched off: no ttl
	if err := n.Enabled(ctx, "shell", nil, now); err != nil {
		t.Fatal(err)
	}
	f.With(func(s *fakesys.Server) {
		if _, ok := s.Notices[system.NoticeID]["ttl"]; ok {
			t.Error("a switch without an end posts a ttl")
		}
	})
	if err := n.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if err := n.Clear(ctx); err != nil {
		t.Fatalf("clearing a notice that is not there: %v", err)
	}
	f.With(func(s *fakesys.Server) {
		if len(s.Notices) != 0 {
			t.Errorf("notices %v", s.Notices)
		}
	})
	// a wrong token is an error the caller logs; no address means no notice and no error
	_ = os.WriteFile(tok, []byte("other\n"), 0o600)
	if err := n.Enabled(ctx, "shell", nil, now); err == nil {
		t.Error("a refused notice is not reported")
	}
	if err := (&system.Notifier{}).Enabled(ctx, "shell", nil, now); err != nil {
		t.Errorf("without an address: %v", err)
	}
	var none *system.Notifier
	if err := none.Clear(ctx); err != nil {
		t.Errorf("a nil notifier: %v", err)
	}
}
