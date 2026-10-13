package video

import (
	"strconv"
	"strings"
	"time"
)

// FFmpeg's progress output signals the first encoded frame. Subtracting its
// output timestamp gives the frame's position in the wall-clock interval.
// Subsequent progress is discarded instead of accumulating per-frame logs.
type captureProgress struct {
	firstFrame   chan time.Time
	line         strings.Builder
	frames       int64
	microseconds int64
	sent         bool
}

func newCaptureProgress() *captureProgress {
	return &captureProgress{firstFrame: make(chan time.Time, 1)}
}

func (p *captureProgress) Write(payload []byte) (int, error) {
	for _, character := range payload {
		if character == '\n' {
			p.readLine(p.line.String())
			p.line.Reset()
		} else if p.line.Len() < 256 {
			p.line.WriteByte(character)
		}
	}
	return len(payload), nil
}

func (p *captureProgress) readLine(line string) {
	if p.sent {
		return
	}
	key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
	if !ok {
		return
	}
	switch key {
	case "frame":
		p.frames, _ = strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	case "out_time_us":
		p.microseconds, _ = strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	case "progress":
		if p.frames > 0 {
			p.sent = true
			elapsed := time.Duration(max(0, p.microseconds)) * time.Microsecond
			p.firstFrame <- time.Now().Add(-elapsed)
		}
	}
}
