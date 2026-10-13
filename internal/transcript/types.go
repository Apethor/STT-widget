// Package transcript is the processing core shared by desktop and CLI.
package transcript

import (
	"context"
	"errors"
	"fmt"

	"ai-talker/internal/audio"
	"ai-talker/internal/config"
)

type Progress struct {
	Stage     string  `json:"stage"`
	Message   string  `json:"message"`
	Fraction  float64 `json:"fraction"`
	Completed int     `json:"completed"`
	Total     int     `json:"total"`
}

type ChunkResult struct {
	Text           string           `json:"text"`
	Segments       []SpeechSegment  `json:"segments,omitempty"`
	Usage          map[string]any   `json:"usage"`
	ElapsedSeconds float64          `json:"elapsed_seconds"`
	ReviewReason   string           `json:"review_reason,omitempty"`
	Engine         string           `json:"engine,omitempty"`
	Children       []Part           `json:"children,omitempty"`
	Recovery       *RecoveryAttempt `json:"recovery,omitempty"`
}

// RecoveryAttempt belongs to one part, including unsuccessful local attempts.
type RecoveryAttempt struct {
	Engine string `json:"engine"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type Part struct {
	Index          int              `json:"index"`
	Start          float64          `json:"start"`
	Duration       float64          `json:"duration"`
	Text           string           `json:"text"`
	Segments       []SpeechSegment  `json:"segments,omitempty"`
	NoSpeech       bool             `json:"no_speech"`
	DetectedSpeech bool             `json:"detected_speech"`
	SpeechSeconds  float64          `json:"speech_seconds"`
	Usage          map[string]any   `json:"usage"`
	ElapsedSeconds float64          `json:"elapsed_seconds"`
	NeedsReview    bool             `json:"needs_review,omitempty"`
	ReviewReason   string           `json:"review_reason,omitempty"`
	Engine         string           `json:"engine,omitempty"`
	Children       []Part           `json:"children,omitempty"`
	Error          string           `json:"error,omitempty"`
	Recovery       *RecoveryAttempt `json:"recovery,omitempty"`
}

// SpeechSegment preserves provider-supplied timing and diarization for one
// utterance inside its containing Part. Start and End are relative to the
// audio represented by that Part.
type SpeechSegment struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker,omitempty"`
	Text    string  `json:"text"`
}

type Result struct {
	Text            string            `json:"text"`
	Source          string            `json:"source"`
	DurationSeconds float64           `json:"duration_seconds"`
	ElapsedSeconds  float64           `json:"elapsed_seconds"`
	ModelID         string            `json:"model_id"`
	Region          string            `json:"region"`
	Parts           []Part            `json:"parts"`
	ReusedParts     int               `json:"reused_parts"`
	JobID           string            `json:"job_id"`
	Exports         map[string]string `json:"exports"`
	NeedsReview     bool              `json:"needs_review"`
}

// Transcriber implementations must bound every call, including credential
// renewal. Sent AWS calls may finish for their cache under a separate deadline.
// Local inference inherits foreground cancellation and stops its process.
type Transcriber interface {
	Transcribe(context.Context, audio.Chunk) (ChunkResult, error)
	Close() error
}

// StreamingTranscriber optionally reports the accumulated text before a part
// finishes. Provisional text is never a completed result or a cache entry.
type StreamingTranscriber interface {
	TranscribeStream(context.Context, audio.Chunk, func(string)) (ChunkResult, error)
}

type Partial struct {
	Index int
	Text  string
}

type Factory func(context.Context, config.Settings) (Transcriber, error)

var ErrOutputLimit = errors.New("limite de saída do motor de transcrição")

type Error struct {
	Cause       error
	PartialText string
	JobID       string
	Result      Result
}

func (e *Error) Error() string {
	return fmt.Sprintf("não foi possível concluir a transcrição: %v", e.Cause)
}

func (e *Error) Unwrap() error { return e.Cause }
