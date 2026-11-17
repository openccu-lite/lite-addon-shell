package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The defaults are the decided ones, off after the install, and the bounds hold.
func TestDefaultsAndBounds(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "conf")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Errorf("the directory is %v", st.Mode().Perm())
	}
	if got := s.Settings(); got != (Settings{IdleMinutes: 15, MaxHours: 8, MaxSessions: 2, AllowTunnel: false}) {
		t.Fatalf("defaults %+v", got)
	}
	if s.Switch().On || s.Switch().Active(time.Now()) {
		t.Fatal("on after the install")
	}
	if DefaultWindow != 8 || !ValidWindow(1) || !ValidWindow(8) || !ValidWindow(0) || ValidWindow(24) || ValidWindow(-1) {
		t.Error("the switch's lengths are 1 h, 8 h and until switched off")
	}
	for _, bad := range []Settings{
		{IdleMinutes: 4, MaxHours: 8, MaxSessions: 2}, {IdleMinutes: 61, MaxHours: 8, MaxSessions: 2},
		{IdleMinutes: 15, MaxHours: 0, MaxSessions: 2}, {IdleMinutes: 15, MaxHours: 13, MaxSessions: 2},
		{IdleMinutes: 15, MaxHours: 8, MaxSessions: 0}, {IdleMinutes: 15, MaxHours: 8, MaxSessions: 5},
	} {
		var ve *ValidationError
		if err := s.PutSettings(bad); !errors.As(err, &ve) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if s.Settings() != Default {
		t.Error("a refused change was stored")
	}
}

// Settings and the switch survive a restart; the files hold no secret and are the addon's alone.
func TestPersist(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	want := Settings{IdleMinutes: 30, MaxHours: 2, MaxSessions: 4, AllowTunnel: true}
	if err := s.PutSettings(want); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	until := now.Add(8 * time.Hour)
	if err := s.SetSwitch(Switch{On: true, Until: &until, By: "admin", At: &now}); err != nil {
		t.Fatal(err)
	}
	s2, _ := Open(dir)
	if s2.Settings() != want {
		t.Fatalf("settings %+v", s2.Settings())
	}
	sw := s2.Switch()
	if !sw.On || !sw.Until.Equal(until) || sw.By != "admin" || !sw.Active(now.Add(time.Hour)) || sw.Active(until) {
		t.Fatalf("switch %+v", sw)
	}
	for _, f := range []string{"settings.json", "state.json"} {
		st, err := os.Stat(filepath.Join(dir, f))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", f, err, st)
		}
		b, _ := os.ReadFile(filepath.Join(dir, f))
		if strings.Contains(strings.ToLower(string(b)), "password") || strings.Contains(strings.ToLower(string(b)), "key") {
			t.Errorf("%s holds a secret's name: %s", f, b)
		}
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".tmp-*"))
	if len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
	// until switched off
	if err := s.SetSwitch(Switch{On: true}); err != nil {
		t.Fatal(err)
	}
	if !s.Switch().Active(now.Add(1000 * time.Hour)) {
		t.Error("a switch without an end ends")
	}
	// a broken file is the default, never a failure to start
	_ = os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "state.json"), []byte("nonsense"), 0o600)
	s3, err := Open(dir)
	if err != nil || s3.Settings() != Default || s3.Switch().On {
		t.Fatalf("broken files: %v %+v %+v", err, s3.Settings(), s3.Switch())
	}
}
