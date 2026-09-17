// Package shadowimport maps caller-supplied review-export JSONL into replay inputs
// and importer-owned channel/TGID evidence. It has no operational authority.
package shadowimport

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"

	"greenwich-fire-responder/backend/internal/shadowprocessor"
)

const (
	maxSources          = 1000
	maxSourceBytes      = 8 * 1024 * 1024
	maxJSONLine         = 1 * 1024 * 1024
	maxInputRecords     = 1000
	maxInputStringBytes = 4 * 1024 * 1024
	datasetIDPrefix     = "gfr-audio-v1:"
)

var fingerprintPattern = regexp.MustCompile(`^pcm-s16le-16000-mono-v1:sha256:[0-9a-f]{64}$`)

var allowedKeys = map[string]bool{
	"dataset_id": true, "audio_fingerprint": true, "transcription_attempt_id": true,
	"channel": true, "tgid": true, "duration_ms": true,
	"raw_model_transcript": true, "human_reference_transcript": true,
	"model": true, "split": true, "reviewed_at": true,
}

type Source struct {
	Bytes    []byte
	TextKind shadowprocessor.TextKind
}

type Record struct {
	Input   shadowprocessor.Input
	Channel string
	TGID    int64
}

// Importer holds no sources, processor, or history. Construct with New before use.
type Importer struct{}

type jsonObject struct {
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

func New() (*Importer, error) {
	return &Importer{}, nil
}

func (im *Importer) Import(sources []Source) ([]Record, error) {
	if len(sources) == 0 {
		return []Record{}, nil
	}
	if err := admitSources(sources); err != nil {
		return nil, err
	}
	records, err := parseAll(sources)
	if err != nil {
		return nil, err
	}
	if err := admitRecords(records); err != nil {
		return nil, err
	}
	return records, nil
}

func admitSources(sources []Source) error {
	if len(sources) > maxSources {
		return errors.New("shadowimport: too many sources")
	}
	for i := range sources {
		if sources[i].TextKind != shadowprocessor.TextRawModel && sources[i].TextKind != shadowprocessor.TextHumanReference {
			return errors.New("shadowimport: invalid text kind")
		}
	}
	for i := range sources {
		if len(sources[i].Bytes) == 0 {
			return errors.New("shadowimport: empty source")
		}
	}
	remaining := maxSourceBytes
	for i := range sources {
		n := len(sources[i].Bytes)
		if _, ok := charge(maxSourceBytes, n); !ok {
			return errors.New("shadowimport: source bytes exceed limit")
		}
		var ok bool
		remaining, ok = charge(remaining, n)
		if !ok {
			return errors.New("shadowimport: source bytes exceed limit")
		}
	}
	return nil
}

func parseAll(sources []Source) ([]Record, error) {
	var out []Record
	for i := range sources {
		if err := parseSource(sources[i], &out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func parseSource(src Source, out *[]Record) error {
	data := src.Bytes
	if bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		return errors.New("shadowimport: invalid source")
	}
	if !utf8.Valid(data) {
		return errors.New("shadowimport: invalid source")
	}
	scan := bufio.NewScanner(bytes.NewReader(data))
	scan.Buffer(make([]byte, 4096), maxSourceBytes+2)
	for scan.Scan() {
		line := scan.Bytes()
		if len(line) == 0 || len(line) > maxJSONLine {
			return errors.New("shadowimport: invalid source")
		}
		copied := append([]byte(nil), line...)
		obj, err := parseLine(copied)
		if err != nil {
			return err
		}
		if len(*out) >= maxInputRecords {
			return errors.New("shadowimport: too many input records")
		}
		*out = append(*out, mapRecord(src.TextKind, obj))
	}
	if scan.Err() != nil {
		return errors.New("shadowimport: invalid source")
	}
	return nil
}

func parseLine(line []byte) (jsonObject, error) {
	var obj jsonObject
	if !utf8.Valid(line) || !json.Valid(line) || !validUnicodeEscapes(line) {
		return obj, errors.New("shadowimport: invalid source")
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return obj, errors.New("shadowimport: invalid source")
	}
	seen := map[string]bool{}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return obj, errors.New("shadowimport: invalid source")
		}
		key, ok := k.(string)
		if !ok || seen[key] || !allowedKeys[key] {
			return obj, errors.New("shadowimport: invalid source")
		}
		seen[key] = true
		var raw json.RawMessage
		if dec.Decode(&raw) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return obj, errors.New("shadowimport: invalid source")
		}
	}
	if _, err = dec.Token(); err != nil {
		return obj, errors.New("shadowimport: invalid source")
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return obj, errors.New("shadowimport: invalid source")
	}
	if len(seen) != 11 {
		return obj, errors.New("shadowimport: invalid source")
	}
	dec = json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if dec.Decode(&obj) != nil {
		return jsonObject{}, errors.New("shadowimport: invalid source")
	}
	sum := md5.Sum([]byte(obj.Fingerprint))
	if !fingerprintPattern.MatchString(obj.Fingerprint) || obj.DatasetID != datasetIDPrefix+hex.EncodeToString(sum[:]) || obj.DurationMS < 0 || obj.ReviewedAt.IsZero() {
		return jsonObject{}, errors.New("shadowimport: invalid source")
	}
	if obj.Split != "train" && obj.Split != "validation" && obj.Split != "test" {
		return jsonObject{}, errors.New("shadowimport: invalid source")
	}
	return obj, nil
}

func mapRecord(kind shadowprocessor.TextKind, obj jsonObject) Record {
	transcript := obj.Raw
	if kind == shadowprocessor.TextHumanReference {
		transcript = obj.Reference
	}
	return Record{
		Input: shadowprocessor.Input{
			Source: shadowprocessor.Source{
				Kind:         shadowprocessor.SourceReplay,
				Reference:    obj.DatasetID,
				TextKind:     kind,
				RecordingRef: obj.Fingerprint,
				AttemptRef:   strconv.FormatInt(obj.AttemptID, 10),
				Model:        obj.Model,
			},
			Transcript: transcript,
		},
		Channel: obj.Channel,
		TGID:    obj.TGID,
	}
}

func admitRecords(records []Record) error {
	if len(records) > maxInputRecords {
		return errors.New("shadowimport: too many input records")
	}
	remaining := maxInputStringBytes
	for i := range records {
		var ok bool
		remaining, ok = chargeInput(remaining, records[i].Input)
		if !ok {
			return errors.New("shadowimport: input string bytes exceed limit")
		}
	}
	return nil
}

func chargeInput(remaining int, in shadowprocessor.Input) (int, bool) {
	for _, n := range [...]int{
		len(in.Source.Kind),
		len(in.Source.Reference),
		len(in.Source.TextKind),
		len(in.Source.RecordingRef),
		len(in.Source.AttemptRef),
		len(in.Source.Model),
		len(in.Transcript),
	} {
		var ok bool
		remaining, ok = charge(remaining, n)
		if !ok {
			return remaining, false
		}
	}
	return remaining, true
}

func charge(remaining, n int) (int, bool) {
	if n > remaining {
		return remaining, false
	}
	return remaining - n, true
}

func validUnicodeEscapes(line []byte) bool {
	for i := 0; i < len(line); i++ {
		if line[i] != '\\' {
			continue
		}
		i++
		if line[i] != 'u' {
			continue
		}
		n, err := strconv.ParseUint(string(line[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(line) || line[i+1] != '\\' || line[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(line[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
