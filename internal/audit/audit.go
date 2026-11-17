// Package audit writes the addon's audit lines: one line per event with fixed fields, to the
// journal (the unit's output) and into a small ring the addon's page shows. A line says who opened
// or closed a session, from where and why, and who switched the addon on or off. Nothing typed in
// a terminal and nothing it showed is ever part of a line: that would put every password typed at
// a prompt into the journal.
package audit

import (
	"fmt"
	"sync"
	"time"
)

// Entry is one line with its time (Unix milliseconds).
type Entry struct {
	At   int64  `json:"at"`
	Line string `json:"line"`
}

// Log is the ring and the sink.
type Log struct {
	mu    sync.Mutex
	lines []Entry
	// Max is how many lines the ring keeps; 200 by default.
	Max int
	// Sink writes a line to the journal.
	Sink func(line string)
	Now  func() time.Time
}

// Printf writes one line.
func (l *Log) Printf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	now := time.Now()
	if l.Now != nil {
		now = l.Now()
	}
	l.mu.Lock()
	max := l.Max
	if max <= 0 {
		max = 200
	}
	l.lines = append(l.lines, Entry{At: now.UnixMilli(), Line: line})
	if over := len(l.lines) - max; over > 0 {
		l.lines = append([]Entry(nil), l.lines[over:]...)
	}
	sink := l.Sink
	l.mu.Unlock()
	if sink != nil {
		sink(line)
	}
}

// List answers the lines, newest first.
func (l *Log) List() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, len(l.lines))
	for i, e := range l.lines {
		out[len(l.lines)-1-i] = e
	}
	return out
}
