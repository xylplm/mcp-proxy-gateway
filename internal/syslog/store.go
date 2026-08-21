package syslog

import (
	"maps"
	"strings"
	"sync"
	"time"
)

const (
	defaultCapacity = 2000
	defaultLimit    = 200
	maxLimit        = 1000
)

// Entry is a process-local runtime log entry exposed to the admin console.
type Entry struct {
	ID      int64          `json:"id"`
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Source  string         `json:"source,omitempty"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

// Store keeps a bounded in-memory ring of runtime logs.
type Store struct {
	mu       sync.RWMutex
	entries  []Entry
	capacity int
	nextID   int64
}

func NewStore(capacity int) *Store {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	return &Store{capacity: capacity}
}

// Add appends a new entry to the ring buffer, returning the stored entry.
// source is the caller's source location (e.g. "service.go:42"); empty when
// unavailable.
func (s *Store) Add(level, message string, at time.Time, source string, attrs map[string]any) Entry {
	if at.IsZero() {
		at = time.Now()
	}
	level = NormalizeLevel(level)
	if len(attrs) == 0 {
		attrs = nil
	}
	if source == "" {
		source = ""
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	entry := Entry{
		ID:      s.nextID,
		Time:    at,
		Level:   level,
		Message: message,
		Source:  source,
		Attrs:   attrs,
	}
	if len(s.entries) >= s.capacity {
		copy(s.entries, s.entries[1:])
		s.entries[len(s.entries)-1] = entry
		return entry
	}
	s.entries = append(s.entries, entry)
	return entry
}

func (s *Store) List(afterID int64, level string, limit int) []Entry {
	level = NormalizeLevel(level)
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Entry, 0, min(limit, len(s.entries)))
	for _, entry := range s.entries {
		if afterID > 0 && entry.ID <= afterID {
			continue
		}
		if level != "" && entry.Level != level {
			continue
		}
		out = append(out, cloneEntry(entry))
		if afterID > 0 && len(out) >= limit {
			break
		}
	}
	if afterID <= 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// Export returns all currently buffered entries matching level.
func (s *Store) Export(level string) []Entry {
	level = NormalizeLevel(level)

	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Entry, 0, len(s.entries))
	for _, entry := range s.entries {
		if level != "" && entry.Level != level {
			continue
		}
		out = append(out, cloneEntry(entry))
	}
	return out
}

func (s *Store) Clear() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := len(s.entries)
	s.entries = nil
	return count
}

func NormalizeLevel(level string) string {
	return strings.ToLower(strings.TrimSpace(level))
}

func ValidLevel(level string) bool {
	switch NormalizeLevel(level) {
	case "", "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

func cloneEntry(entry Entry) Entry {
	if len(entry.Attrs) == 0 {
		return entry
	}
	attrs := make(map[string]any, len(entry.Attrs))
	maps.Copy(attrs, entry.Attrs)
	entry.Attrs = attrs
	return entry
}
