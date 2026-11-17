// Package system is what the addon asks of openccu-lite, on the loopback: whether a web session
// still stands (GET /api/auth/v1/state, with ?peek=1 so that asking does not keep the session
// alive), and the notice on the Status page while the addon is switched on (the system's addon
// notices, with the addon's control token). The addon holds no API scope and calls nothing else.
package system

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// State is the system's answer about a web session.
type State struct {
	Authenticated bool   `json:"authenticated"`
	SID           string `json:"sid"`
	User          string `json:"user"`
	Level         string `json:"level"`
	// AuthOff says the system runs without a login: everyone is an administrator.
	AuthOff bool `json:"auth_off"`
	// Peek is true when the system knew ?peek=1 (openccu-lite 2.0.0-beta.7): the question did
	// not refresh the session's idle timer.
	Peek bool `json:"peek"`
}

// Admin says whether the answer confirms sid as an administrator's session.
func (s State) Admin(sid string) bool {
	return s.Authenticated && s.SID != "" && s.SID == sid && s.Level == "administer"
}

// sidShape is a session id as openccu-lite mints it; nothing else is ever sent to the system.
var sidShape = regexp.MustCompile(`^[A-Z2-7]{26}$`)

// ErrShape is answered for a credential that is not a session id (a token, an alias).
var ErrShape = errors.New("not a session id")

// Client asks the system.
type Client struct {
	// Base is the system's web server, http://127.0.0.1.
	Base string
	HTTP *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 5 * time.Second}
}

// Session asks about a web session. With peek the question does not count as the session's
// activity (State.Peek says whether the system honoured that). An error means the system could
// not be asked, not that the session is gone.
func (c *Client) Session(ctx context.Context, sid string, peek bool) (State, error) {
	var st State
	if !sidShape.MatchString(sid) {
		return st, ErrShape
	}
	url := strings.TrimRight(c.Base, "/") + "/api/auth/v1/state"
	if peek {
		url += "?peek=1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return st, err
	}
	req.Header.Set("Authorization", "Bearer "+sid)
	res, err := c.http().Do(req)
	if err != nil {
		return st, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
		return st, fmt.Errorf("the system answered HTTP %d", res.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&st); err != nil {
		return st, err
	}
	return st, nil
}

// ControlTokenDir is where the system writes every addon's control token (the one its notices take).
const ControlTokenDir = "/run/occulite/addon-tokens"

// Notifier posts and clears the addon's notice on the system's Status page.
type Notifier struct {
	// Base is the system service's loopback address (http://127.0.0.1:8183); "" posts nothing.
	Base string
	// TokenFile is the addon's control token.
	TokenFile string
	HTTP      *http.Client
}

// NoticeID is the one notice the addon posts.
const NoticeID = "enabled"

// Enabled posts the notice that the root shell is switched on; until nil means until it is
// switched off. The notice ends by itself at until, should the daemon not be there to clear it.
func (n *Notifier) Enabled(ctx context.Context, addonID string, until *time.Time, now time.Time) error {
	if n == nil || n.Base == "" {
		return nil
	}
	title := map[string]string{
		"de": "Die Root-Shell im Browser ist eingeschaltet",
		"en": "The root shell in the browser is switched on",
	}
	text := map[string]string{
		"de": "Administratoren können über das Addon Shell eine Root-Sitzung im Browser öffnen, mit dem root-Passwort. Schalten Sie das Addon aus, wenn Sie die Shell nicht brauchen.",
		"en": "Administrators can open a root session in the browser through the Shell addon, with root's password. Switch the addon off when you do not need the shell.",
	}
	body := map[string]any{"severity": "warning", "title": title, "text": text, "link": "/addon-settings/" + addonID}
	if until != nil {
		ttl := int(until.Sub(now).Seconds()) + 60
		if ttl < 60 {
			ttl = 60
		}
		body["ttl"] = ttl
	}
	return n.call(ctx, http.MethodPut, body)
}

// Clear removes the notice; one that is not there is cleared.
func (n *Notifier) Clear(ctx context.Context) error {
	if n == nil || n.Base == "" {
		return nil
	}
	return n.call(ctx, http.MethodDelete, nil)
}

func (n *Notifier) call(ctx context.Context, method string, body any) error {
	tok, err := os.ReadFile(n.TokenFile)
	if err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(n.Base, "/")+"/api/system/v1/addon-notices/"+NoticeID, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := n.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
	if res.StatusCode/100 == 2 || method == http.MethodDelete && res.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("the system answered HTTP %d", res.StatusCode)
}
