// Package transcriptreview stores explicit human submissions, never inferred truth.
package transcriptreview

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxTextBytes = 65536
const MaxExportRecords = 1000

var (
	ErrInput       = errors.New("invalid review input or unsafe file target; see transcript-review documentation")
	ErrUsage       = errors.New("usage: transcript-review queue|show|submit|history|stats|export (see backend/README.md)")
	ErrUnavailable = errors.New("review database unavailable or command deadline exceeded")
	ErrIneligible  = errors.New("unknown or ineligible recording/attempt, or invalid review")
	ErrExists      = errors.New("export target exists; explicit --overwrite is required")
)

type Candidate struct {
	TransmissionID int64  `json:"transmission_id"`
	AttemptID      int64  `json:"transcription_attempt_id"`
	Channel        string `json:"channel"`
	TGID           int64  `json:"tgid"`
	DurationMS     int64  `json:"duration_ms"`
	Model          string `json:"model"`
	Verdict        string `json:"latest_verdict"`
}
type Detail struct {
	Candidate
	Fingerprint string `json:"audio_fingerprint"`
	SourcePath  string `json:"source_path"`
	Raw         string `json:"raw_model_transcript"`
}
type Submission struct {
	TransmissionID, AttemptID                   int64
	Reviewer, Verdict, Reference, Reason, Notes string
}
type Review struct {
	ID         int64     `json:"review_id"`
	AttemptID  int64     `json:"transcription_attempt_id"`
	Verdict    string    `json:"verdict"`
	Reviewer   string    `json:"reviewer_label"`
	Reference  string    `json:"reference_transcript"`
	Reason     string    `json:"exclusion_reason"`
	Notes      string    `json:"notes"`
	ReviewedAt time.Time `json:"reviewed_at"`
}
type Stats struct {
	Accepted   int64 `json:"accepted"`
	Excluded   int64 `json:"excluded"`
	Followup   int64 `json:"needs_followup"`
	Unreviewed int64 `json:"unreviewed"`
	Train      int64 `json:"accepted_train"`
	Validation int64 `json:"accepted_validation"`
	Test       int64 `json:"accepted_test"`
}

// Deliberate export allowlist. No source name/path, RID, reviewer, notes, settings,
// errors, authentication, or provider location can be serialized from this type.
type ExportRecord struct {
	DatasetID   string    `json:"dataset_id"`
	Fingerprint string    `json:"audio_fingerprint"`
	AttemptID   int64     `json:"transcription_attempt_id"`
	Channel     string    `json:"channel"`
	TGID        int64     `json:"tgid"`
	DurationMS  int64     `json:"duration_ms"`
	Raw         string    `json:"raw_model_transcript"`
	Reference   string    `json:"human_reference_transcript"`
	Model       string    `json:"model"`
	Split       string    `json:"split"`
	ReviewedAt  time.Time `json:"reviewed_at"`
}
type Store interface {
	Queue(context.Context, int) ([]Candidate, error)
	Show(context.Context, int64) (Detail, error)
	Submit(context.Context, Submission) (Review, error)
	History(context.Context, int64, int) ([]Review, error)
	Stats(context.Context) (Stats, error)
	Export(context.Context, int, func(ExportRecord) error) error
}

func validText(s string, max int, multiline bool) bool {
	return len(s) <= max && utf8.ValidString(s) && strings.IndexFunc(s, func(r rune) bool {
		if multiline && (r == '\n' || r == '\r' || r == '\t') {
			return false
		}
		return unicode.IsControl(r) || unicode.In(r, unicode.Cf)
	}) < 0
}
func (s Submission) Validate() error {
	if s.TransmissionID < 1 || s.AttemptID < 1 || strings.TrimSpace(s.Reviewer) == "" || !validText(s.Reviewer, 80, false) || !validText(s.Reference, MaxTextBytes, true) || !validText(s.Reason, 1024, true) || !validText(s.Notes, 4096, true) {
		return ErrInput
	}
	switch s.Verdict {
	case "accepted":
		if strings.TrimSpace(s.Reference) == "" {
			return ErrInput
		}
	case "excluded":
		if strings.TrimSpace(s.Reason) == "" {
			return ErrInput
		}
	case "needs_followup":
	default:
		return ErrInput
	}
	return nil
}
