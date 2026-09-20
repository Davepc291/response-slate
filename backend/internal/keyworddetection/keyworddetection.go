// Package keyworddetection is a deterministic, synthetic text-evidence
// source over already-completed transcript text (docs/alert-notification-engine-v1.md
// Section 4). It never discovers or reads a recording, never invokes
// transcription, and never calls dispatch-interpretation or unit-status
// resolution logic. Detectors propose evidence only and never mutate state.
package keyworddetection

import (
	"errors"
	"regexp"
	"strings"
	"unicode"

	"greenwich-fire-responder/backend/internal/alerts"
)

const (
	// maxTranscriptBytes matches shadowprocessor's existing
	// transcript_too_large bound, so both packages reject oversized
	// transcripts the same way.
	maxTranscriptBytes = 65536
	maxKeywordsPerList = 200
	maxContextPerEntry = 20
	maxPhraseBytes     = 200
	maxListNameBytes   = 128
	maxContextWindow   = 500
)

var (
	idPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	namePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _-]{0,127}$`)
	phrasePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 '-]{0,199}$`)
)

// Keyword is one entry in a named list. RequiredContext (if non-empty) means
// at least one listed phrase must also appear for the match to be firm.
// ExcludedContext (if non-empty) suppresses an otherwise-matching keyword
// when any listed phrase appears. ContextWindow, in tokens, bounds how far
// from the keyword occurrence the context phrase may appear; 0 means
// anywhere in the transcript.
type Keyword struct {
	Phrase          string
	RequiredContext []string
	ExcludedContext []string
	ContextWindow   int
}

// List is a named keyword list, configurable per talkgroup/channel.
type List struct {
	ID       string
	Name     string
	Keywords []Keyword
	Channels []alerts.Channel
}

// Transcript is already-completed, already-materialized transcript text. No
// detector in this package reads, requests, or waits for a transcript
// itself.
type Transcript struct {
	Text string
}

// Match is one keyword's evaluation result. State is either StateMatched or
// StateAmbiguous: keyword detection never produces StatePartial or
// StateLowConfidence, which are tone-detection-specific reason codes.
type Match struct {
	ListID      string
	Phrase      string
	State       alerts.State
	Occurrences int
}

// Detector is stateless; construct with New before use.
type Detector struct{}

func New() *Detector { return &Detector{} }

func validatePhrase(s string) error {
	if !phrasePattern.MatchString(s) {
		return errors.New("keyworddetection: phrase is missing or contains unsupported characters")
	}
	return nil
}

func validateList(list List) error {
	if !idPattern.MatchString(list.ID) {
		return errors.New("keyworddetection: invalid list id")
	}
	if !namePattern.MatchString(list.Name) || len(list.Name) > maxListNameBytes {
		return errors.New("keyworddetection: invalid list name")
	}
	if len(list.Keywords) < 1 || len(list.Keywords) > maxKeywordsPerList {
		return errors.New("keyworddetection: unsupported keyword count")
	}
	if len(list.Channels) < 1 || len(list.Channels) > 4 {
		return errors.New("keyworddetection: unsupported channel count")
	}
	seenChannel := map[alerts.Channel]bool{}
	for _, c := range list.Channels {
		switch c {
		case alerts.ChannelCH1A, alerts.ChannelCH2B, alerts.ChannelCH3B, alerts.ChannelCH4C:
		default:
			return errors.New("keyworddetection: invalid channel")
		}
		if seenChannel[c] {
			return errors.New("keyworddetection: duplicate channel")
		}
		seenChannel[c] = true
	}
	for _, kw := range list.Keywords {
		if err := validatePhrase(kw.Phrase); err != nil || len(kw.Phrase) > maxPhraseBytes {
			return errors.New("keyworddetection: invalid keyword phrase")
		}
		if len(kw.RequiredContext) > maxContextPerEntry || len(kw.ExcludedContext) > maxContextPerEntry {
			return errors.New("keyworddetection: unsupported context count")
		}
		for _, c := range append(append([]string{}, kw.RequiredContext...), kw.ExcludedContext...) {
			if err := validatePhrase(c); err != nil || len(c) > maxPhraseBytes {
				return errors.New("keyworddetection: invalid context phrase")
			}
		}
		if kw.ContextWindow < 0 || kw.ContextWindow > maxContextWindow {
			return errors.New("keyworddetection: context window out of range")
		}
	}
	return nil
}

func validateTranscript(t Transcript) error {
	if len(t.Text) > maxTranscriptBytes {
		return errors.New("keyworddetection: transcript exceeds maximum bounded size")
	}
	if strings.TrimSpace(t.Text) == "" {
		return errors.New("keyworddetection: empty transcript is an absent evidence class, not a negative result")
	}
	return nil
}

func channelConfigured(list List, channel alerts.Channel) bool {
	for _, c := range list.Channels {
		if c == channel {
			return true
		}
	}
	return false
}

// Detect matches every keyword in list against t for channel. If list is not
// configured for channel, it returns (nil, nil): the caller's per-talkgroup
// configuration decided this list does not apply here, which is not an
// error.
func (d *Detector) Detect(list List, channel alerts.Channel, t Transcript) ([]Match, error) {
	if err := validateList(list); err != nil {
		return nil, err
	}
	if err := validateTranscript(t); err != nil {
		return nil, err
	}
	if !channelConfigured(list, channel) {
		return nil, nil
	}
	tokens := tokenize(t.Text)
	results := make([]Match, 0, len(list.Keywords))
	for _, kw := range list.Keywords {
		if m, ok := evaluate(list.ID, kw, tokens); ok {
			results = append(results, m)
		}
	}
	return results, nil
}

// tokenize reproduces transcripteval's normalization exactly: lowercase,
// then split on any rune that is not a letter or number, so punctuation is a
// separator rather than deleted ("Engine 2, on-scene!" -> "engine 2 on
// scene"). Matching a phrase against this token sequence gives whole-word,
// case-insensitive matching for free: "clear" cannot match "unclear" or
// "cleared" because they tokenize to different tokens.
func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

// occurrences returns every start index in tokens where phrase matches
// consecutively.
func occurrences(tokens, phrase []string) []int {
	var idx []int
	for i := 0; i+len(phrase) <= len(tokens); i++ {
		match := true
		for j := range phrase {
			if tokens[i+j] != phrase[j] {
				match = false
				break
			}
		}
		if match {
			idx = append(idx, i)
		}
	}
	return idx
}

func contextPresent(tokens []string, occ []int, ctx []string, window, phraseLen int) bool {
	if len(ctx) == 0 {
		return false
	}
	if window <= 0 {
		return len(occurrences(tokens, ctx)) > 0
	}
	for _, start := range occ {
		lo := max(0, start-window)
		hi := min(len(tokens), start+phraseLen+window)
		if len(occurrences(tokens[lo:hi], ctx)) > 0 {
			return true
		}
	}
	return false
}

// evaluate classifies one keyword. A bare match with satisfied required
// context and no excluded context present is StateMatched. Every other case
// where the phrase occurs at all (unmet required context, present excluded
// context, or both) is StateAmbiguous: unresolved context is recorded, never
// silently promoted to a firm match and never silently dropped, matching
// Section 4's ambiguity-handling requirement.
func evaluate(listID string, kw Keyword, tokens []string) (Match, bool) {
	phrase := tokenize(kw.Phrase)
	occ := occurrences(tokens, phrase)
	if len(occ) == 0 {
		return Match{}, false
	}

	requiredMet := len(kw.RequiredContext) == 0
	for _, c := range kw.RequiredContext {
		if contextPresent(tokens, occ, tokenize(c), kw.ContextWindow, len(phrase)) {
			requiredMet = true
			break
		}
	}
	excludedPresent := false
	for _, c := range kw.ExcludedContext {
		if contextPresent(tokens, occ, tokenize(c), kw.ContextWindow, len(phrase)) {
			excludedPresent = true
			break
		}
	}

	state := alerts.StateAmbiguous
	if requiredMet && !excludedPresent {
		state = alerts.StateMatched
	}
	return Match{ListID: listID, Phrase: kw.Phrase, State: state, Occurrences: len(occ)}, true
}
