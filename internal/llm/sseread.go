package llm

import (
	"bufio"
	"io"
	"strings"
)

// SSEEvent represents a single Server-Sent Event with an optional event type.
type SSEEvent struct {
	Type string // the "event:" field (empty if not present)
	Data string // the "data:" field payload
}

// SSEScanner reads Server-Sent Events from an io.Reader.
// It yields one SSEEvent per event block, stopping when the reader is
// exhausted or a "data: [DONE]" sentinel is encountered.
type SSEScanner struct {
	scanner   *bufio.Scanner
	current   SSEEvent
	err       error
	done      bool
	eventType string
	dataBuf   strings.Builder
}

// NewSSEScanner creates a scanner over the given reader.
func NewSSEScanner(r io.Reader) *SSEScanner {
	return &SSEScanner{scanner: bufio.NewScanner(r)}
}

// Next advances to the next SSE event. Returns false when no more events
// are available (either EOF or [DONE] sentinel).
func (s *SSEScanner) Next() bool {
	if s.done {
		return false
	}

	s.dataBuf.Reset()
	s.eventType = ""
	hasData := false

	for s.scanner.Scan() {
		line := s.scanner.Text()

		// Empty line signals end of an event block.
		if line == "" {
			if hasData {
				data := s.dataBuf.String()
				if data == "[DONE]" {
					s.done = true
					return false
				}
				s.current = SSEEvent{Type: s.eventType, Data: data}
				return true
			}
			continue
		}

		// Skip SSE comments.
		if strings.HasPrefix(line, ":") {
			continue
		}

		if strings.HasPrefix(line, "event:") {
			s.eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}

		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimPrefix(line, "data:")
			payload = strings.TrimPrefix(payload, " ") // optional single space after "data:"
			if hasData {
				s.dataBuf.WriteString("\n")
			}
			s.dataBuf.WriteString(payload)
			hasData = true
		}
	}

	// Handle final event at EOF (no trailing blank line).
	if hasData {
		data := s.dataBuf.String()
		if data == "[DONE]" {
			s.done = true
			return false
		}
		s.current = SSEEvent{Type: s.eventType, Data: data}
		return true
	}

	s.err = s.scanner.Err()
	return false
}

// Event returns the most recently scanned SSE event.
func (s *SSEScanner) Event() SSEEvent {
	return s.current
}

// Err returns the first non-EOF error encountered by the scanner.
func (s *SSEScanner) Err() error {
	return s.err
}
