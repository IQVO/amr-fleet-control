package mqtt

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
)

// logSink records JSON log lines (every level) for assertions.
type logSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *logSink) logger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(s, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// records returns the logged records whose message contains msg.
func (s *logSink) records(msg string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s.buf.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil && strings.Contains(m["msg"].(string), msg) {
			out = append(out, m)
		}
	}
	return out
}
