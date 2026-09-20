// Package alerts defines the versioned alert-event-v1 model approved by
// docs/alert-notification-engine-v1.md Section 5. An Event is a small,
// safe-to-display evidence record. It has no persistence, network access, or
// operational authority: it cannot create an incident, modify unit status,
// or write to any table owned by the existing radio-status/incident schema.
package alerts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"greenwich-fire-responder/backend/internal/audioanalysis"
)

// SchemaVersion is the fixed value for this contract generation.
const SchemaVersion = "alert-event-v1"

type SourceKind string

const (
	SourceSynthetic    SourceKind = "synthetic"
	SourceShadowReplay SourceKind = "shadow_replay"
)

type Channel string

const (
	ChannelCH1A Channel = "CH1A"
	ChannelCH2B Channel = "CH2B"
	ChannelCH3B Channel = "CH3B"
	ChannelCH4C Channel = "CH4C"
)

// channelTGID mirrors the FR-1 four-channel mapping. A detection's TGID is
// evidence only; it never selects a dispatch role or incident-creation
// eligibility beyond this label.
var channelTGID = map[Channel]int64{
	ChannelCH1A: 57201,
	ChannelCH2B: 57202,
	ChannelCH3B: 57203,
	ChannelCH4C: 57204,
}

type DetectorKind string

const (
	DetectorTone    DetectorKind = "tone"
	DetectorKeyword DetectorKind = "keyword"
)

type State string

const (
	StateMatched       State = "matched"
	StateAmbiguous     State = "ambiguous"
	StatePartial       State = "partial"
	StateLowConfidence State = "low_confidence"
)

// stateWord renders State into the display_summary template, matching the
// contract's worked example: "CH1A tone match: quick-call alert (synthetic).
// NOT LIVE CAD."
var stateWord = map[State]string{
	StateMatched:       "match",
	StateAmbiguous:     "ambiguous match",
	StatePartial:       "partial match",
	StateLowConfidence: "low-confidence match",
}

const maxDisplaySummaryLen = 256

var (
	labelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _-]{0,79}$`)
	idPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	hexPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Evidence identifies the source recording using only the opaque identity
// primitives the existing schema already establishes (Section 2). Never a
// filesystem path, never opened by this package, never any other field.
type Evidence struct {
	// AudioFingerprint is audioanalysis.FingerprintPrefix + 64 lowercase hex
	// digits. Present when audio content has been analyzed.
	AudioFingerprint string
	// SourceIdentity is a 64 lowercase hex digit SHA-256 value produced by
	// recordings.SourceIdentity. Present for metadata-only ingestion.
	SourceIdentity string
}

// key returns the opaque identity string used to build DedupKey and EventID.
// Exactly one of AudioFingerprint or SourceIdentity must be set.
func (e Evidence) key() (string, error) {
	fp, si := e.AudioFingerprint != "", e.SourceIdentity != ""
	if fp == si {
		return "", errors.New("alerts: exactly one of audio_fingerprint or source_identity is required")
	}
	if fp {
		hash := strings.TrimPrefix(e.AudioFingerprint, audioanalysis.FingerprintPrefix)
		if hash == e.AudioFingerprint || !hexPattern.MatchString(hash) {
			return "", errors.New("alerts: invalid audio_fingerprint format")
		}
		return "audio_fingerprint:" + hash, nil
	}
	if !hexPattern.MatchString(e.SourceIdentity) {
		return "", errors.New("alerts: invalid source_identity format")
	}
	return "source_identity:" + e.SourceIdentity, nil
}

// Input carries only contract-approved safe fields (Section 5). It has no
// transcript, recording path, raw audio, credential, dataset identifier, or
// evaluation-pair identifier field, and never will: those are excluded by
// the type itself, not by a runtime check.
type Input struct {
	CreatedAt     time.Time
	ExpiresAt     time.Time
	SourceKind    SourceKind
	Channel       Channel
	TGID          int64
	DetectorKind  DetectorKind
	ToneSetID     string // required iff DetectorKind == DetectorTone
	KeywordListID string // required iff DetectorKind == DetectorKeyword
	Confidence    *float64
	State         State
	Evidence      Evidence
	// Label names the tone-set or keyword-list a human would recognize (for
	// example "quick-call alert"). Bounded, allow-listed charset: it can
	// never carry transcript text, a path, or a credential.
	Label string
}

// Event is the immutable, safe-to-display record a future relay or
// preference system may read. Every field is safe to log or push.
type Event struct {
	EventID        string
	SchemaVersion  string
	CreatedAt      time.Time
	ExpiresAt      time.Time
	SourceKind     SourceKind
	Channel        Channel
	TGID           int64
	DetectorKind   DetectorKind
	ToneSetID      string
	KeywordListID  string
	Confidence     *float64
	State          State
	Synthetic      bool
	ShadowOnly     bool
	DedupKey       string
	DisplaySummary string
}

// Limits are caller-supplied validation bounds. This package assumes no
// default of its own: the actual cooldown/expiration policy for alert events
// is an unresolved question (Section 14, items 3-4), and picking a number
// here would silently invent an operational value the contract explicitly
// leaves open. A future caller (the Step 8C configuration layer, or a test)
// must decide and supply it.
type Limits struct {
	// MaxExpiryWindow bounds how far ExpiresAt may be after CreatedAt. Must
	// be positive: this package will not construct a Builder without an
	// explicit, caller-chosen ceiling.
	MaxExpiryWindow time.Duration
}

// Builder validates and constructs Events under one caller-supplied set of
// Limits. Construct with NewBuilder before use. A constructed Builder
// supports concurrent New calls and retains no call data.
type Builder struct {
	limits Limits
}

func NewBuilder(limits Limits) (*Builder, error) {
	if limits.MaxExpiryWindow <= 0 {
		return nil, errors.New("alerts: max_expiry_window must be a positive, explicitly chosen duration")
	}
	return &Builder{limits: limits}, nil
}

// New validates in and constructs a synthetic, shadow-only Event. Synthetic
// and ShadowOnly are always true: this package never produces a live event.
func (b *Builder) New(in Input) (Event, error) {
	if err := validate(in, b.limits); err != nil {
		return Event{}, err
	}
	evidenceKey, err := in.Evidence.key()
	if err != nil {
		return Event{}, err
	}
	setID := in.ToneSetID
	if in.DetectorKind == DetectorKeyword {
		setID = in.KeywordListID
	}
	summary := displaySummary(in.Channel, in.DetectorKind, in.State, in.Label)
	if len(summary) > maxDisplaySummaryLen {
		return Event{}, errors.New("alerts: display summary exceeds bound")
	}

	// Confidence is copied into a fresh pointer so a caller mutating the
	// float64 behind in.Confidence after this call cannot retroactively
	// change the returned, supposedly-immutable Event.
	var confidence *float64
	if in.Confidence != nil {
		c := *in.Confidence
		confidence = &c
	}

	return Event{
		EventID:        eventID(in),
		SchemaVersion:  SchemaVersion,
		CreatedAt:      in.CreatedAt,
		ExpiresAt:      in.ExpiresAt,
		SourceKind:     in.SourceKind,
		Channel:        in.Channel,
		TGID:           in.TGID,
		DetectorKind:   in.DetectorKind,
		ToneSetID:      in.ToneSetID,
		KeywordListID:  in.KeywordListID,
		Confidence:     confidence,
		State:          in.State,
		Synthetic:      true,
		ShadowOnly:     true,
		DedupKey:       dedupKey(evidenceKey, in.DetectorKind, setID),
		DisplaySummary: summary,
	}, nil
}

func validate(in Input, limits Limits) error {
	if in.CreatedAt.IsZero() {
		return errors.New("alerts: created_at is required")
	}
	// ExpiresAt must be strictly after CreatedAt: a structural requirement
	// of what "expiry" means, not a policy value. The maximum gap is the
	// caller's own Limits, never a value this package assumes.
	if in.ExpiresAt.IsZero() || !in.ExpiresAt.After(in.CreatedAt) {
		return errors.New("alerts: expires_at must be after created_at")
	}
	if in.ExpiresAt.Sub(in.CreatedAt) > limits.MaxExpiryWindow {
		return errors.New("alerts: expires_at exceeds the caller-configured maximum window")
	}
	if in.SourceKind != SourceSynthetic && in.SourceKind != SourceShadowReplay {
		return errors.New("alerts: unsupported source_kind")
	}
	want, ok := channelTGID[in.Channel]
	if !ok {
		return errors.New("alerts: unsupported channel")
	}
	if in.TGID != want {
		return errors.New("alerts: tgid does not match channel")
	}
	switch in.DetectorKind {
	case DetectorTone:
		if !idPattern.MatchString(in.ToneSetID) || in.KeywordListID != "" {
			return errors.New("alerts: tone detections require tone_set_id and no keyword_list_id")
		}
	case DetectorKeyword:
		if !idPattern.MatchString(in.KeywordListID) || in.ToneSetID != "" {
			return errors.New("alerts: keyword detections require keyword_list_id and no tone_set_id")
		}
	default:
		return errors.New("alerts: unsupported detector_kind")
	}
	if in.Confidence != nil {
		c := *in.Confidence
		if math.IsNaN(c) || c < 0 || c > 1 {
			return errors.New("alerts: confidence must be between 0 and 1")
		}
	}
	switch in.State {
	case StateMatched, StateAmbiguous, StatePartial, StateLowConfidence:
	default:
		return errors.New("alerts: unsupported state")
	}
	if !labelPattern.MatchString(in.Label) {
		return errors.New("alerts: label is missing or contains unsupported characters")
	}
	return nil
}

func displaySummary(ch Channel, kind DetectorKind, state State, label string) string {
	return fmt.Sprintf("%s %s %s: %s (synthetic). NOT LIVE CAD.", ch, kind, stateWord[state], label)
}

// dedupKey is a deterministic function of (source recording identity,
// detector_kind, tone_set_id or keyword_list_id), exactly as Section 6
// defines it. Republishing the same detection always produces the same key,
// which is what makes downstream publishing idempotent.
func dedupKey(evidenceKey string, kind DetectorKind, setID string) string {
	sum := sha256.Sum256([]byte("alert-event-dedup-v1\x00" + evidenceKey + "\x00" + string(kind) + "\x00" + setID))
	return hex.EncodeToString(sum[:])
}

// eventID is deterministic: a domain-separated hash of only the non-private
// safe Input fields, so identical input always produces an identical
// event_id while staying structurally distinct from dedup_key (a different,
// unrelated hash under a different domain tag). Section 5 requires event_id
// be "server-generated" and "never derived from private evidence"; it does
// not require randomness, but it does require excluding evidence. This
// function therefore takes no Evidence-derived value at all — not
// AudioFingerprint, not SourceIdentity, and not the evidenceKey/DedupKey
// computed from them, which are used only inside dedupKey, never here. Every
// field folded in below is already a contract-approved safe field, never
// transcript text, a path, a dataset/recording identifier, or a credential.
// Timestamps are normalized to UTC RFC3339Nano so the same instant hashes
// identically regardless of the caller's chosen Location.
func eventID(in Input) string {
	confidence := "none"
	if in.Confidence != nil {
		confidence = strconv.FormatFloat(*in.Confidence, 'g', -1, 64)
	}
	parts := []string{
		"alert-event-id-v1",
		string(in.SourceKind),
		string(in.Channel),
		strconv.FormatInt(in.TGID, 10),
		string(in.DetectorKind),
		in.ToneSetID,
		in.KeywordListID,
		confidence,
		string(in.State),
		in.Label,
		in.CreatedAt.UTC().Format(time.RFC3339Nano),
		in.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "evt_" + hex.EncodeToString(sum[:])
}
