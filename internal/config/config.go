// Package config keeps the addon's settings and the state of its switch in the configuration
// directory (/usr/local/etc/config/addons/shell, 0700): settings.json with the limits, state.json
// with whether the addon is switched on and until when. Neither holds a secret: root's password is
// asked for each session and never written.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Settings are the limits an administrator sets on the addon's page.
type Settings struct {
	// IdleMinutes ends a session after this long without input (output does not count).
	IdleMinutes int `json:"idle_minutes"`
	// MaxHours is the longest a session lasts.
	MaxHours int `json:"max_hours"`
	// MaxSessions is how many sessions are open at once, over all accounts.
	MaxSessions int `json:"max_sessions"`
	// AllowTunnel admits requests that came in through a tunnel addon's web ingress.
	AllowTunnel bool `json:"allow_tunnel"`
	// FilesReadOnly makes the files page read-only altogether: it shows and downloads, and
	// changes nothing.
	FilesReadOnly bool `json:"files_read_only"`
}

// The defaults and the bounds: idle 15 minutes, the longest session 8 hours, at most 2 sessions at
// once, tunnels refused.
var (
	Default = Settings{IdleMinutes: 15, MaxHours: 8, MaxSessions: 2}
	Min     = Settings{IdleMinutes: 5, MaxHours: 1, MaxSessions: 1}
	Max     = Settings{IdleMinutes: 60, MaxHours: 12, MaxSessions: 4}
)

// Windows are the lengths the switch offers, in hours; 0 is "until I switch it off".
var Windows = []int{1, 8, 0}

// DefaultWindow is the switch's default length in hours.
const DefaultWindow = 8

// ValidationError names the field that is out of bounds.
type ValidationError struct{ Field, Message string }

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

// Validate checks the bounds.
func (s Settings) Validate() error {
	check := func(field string, v, lo, hi int) error {
		if v < lo || v > hi {
			return &ValidationError{Field: field, Message: fmt.Sprintf("%d is not between %d and %d", v, lo, hi)}
		}
		return nil
	}
	return errors.Join(
		check("idle_minutes", s.IdleMinutes, Min.IdleMinutes, Max.IdleMinutes),
		check("max_hours", s.MaxHours, Min.MaxHours, Max.MaxHours),
		check("max_sessions", s.MaxSessions, Min.MaxSessions, Max.MaxSessions),
	)
}

// Idle and Longest are the limits as durations.
func (s Settings) Idle() time.Duration    { return time.Duration(s.IdleMinutes) * time.Minute }
func (s Settings) Longest() time.Duration { return time.Duration(s.MaxHours) * time.Hour }

// Switch is the addon's on/off state. Off after the install.
type Switch struct {
	On bool `json:"on"`
	// Until is when the addon switches itself off again; nil while On means until switched off.
	Until *time.Time `json:"until,omitempty"`
	// By and At say who switched it on and when.
	By string     `json:"by,omitempty"`
	At *time.Time `json:"at,omitempty"`
}

// Active says whether the addon is on at now.
func (s Switch) Active(now time.Time) bool {
	return s.On && (s.Until == nil || now.Before(*s.Until))
}

// Store reads and writes the two files.
type Store struct {
	dir string
	mu  sync.Mutex
	set Settings
	sw  Switch
}

// Open reads the directory's files; a missing or unreadable file is the default (off).
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, set: Default}
	var set Settings
	if readJSON(filepath.Join(dir, "settings.json"), &set) == nil && set.Validate() == nil {
		s.set = set
	}
	var sw Switch
	if readJSON(filepath.Join(dir, "state.json"), &sw) == nil {
		s.sw = sw
	}
	return s, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// writeJSON writes atomically: a temporary file beside the target, then a rename.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Settings answers the settings in force.
func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set
}

// PutSettings validates and stores.
func (s *Store) PutSettings(set Settings) error {
	if err := set.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeJSON(filepath.Join(s.dir, "settings.json"), set); err != nil {
		return err
	}
	s.set = set
	return nil
}

// Switch answers the switch as stored (see Active for whether its time is up).
func (s *Store) Switch() Switch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sw
}

// SetSwitch stores the switch.
func (s *Store) SetSwitch(sw Switch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeJSON(filepath.Join(s.dir, "state.json"), sw); err != nil {
		return err
	}
	s.sw = sw
	return nil
}

// ValidWindow says whether hours is one of the lengths the switch offers.
func ValidWindow(hours int) bool {
	for _, w := range Windows {
		if w == hours {
			return true
		}
	}
	return false
}
