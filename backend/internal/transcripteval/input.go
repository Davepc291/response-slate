package transcripteval

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxFileBytes = 64 << 20
	MaxLineBytes = 1 << 20
	MaxTextBytes = 65536
	MaxRecords   = 1000
	MaxTokens    = 2048
	MaxCells     = 20000000
)

var ErrInput = errors.New("invalid or unsafe dataset; see offline evaluation documentation")
var fingerprintPattern = regexp.MustCompile(`^pcm-s16le-16000-mono-v1:sha256:[0-9a-f]{64}$`)
var modelPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// This independent allowlist mirrors the review export; it imports no database code.
type record struct {
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

func validText(s string) bool {
	return len(s) <= MaxTextBytes && utf8.ValidString(s) && strings.IndexFunc(s, func(r rune) bool {
		return (unicode.IsControl(r) && r != '\t' && r != '\r' && r != '\n') || unicode.In(r, unicode.Cf)
	}) < 0
}
func parse(line []byte) (record, error) {
	var r record
	if !utf8.Valid(line) || !json.Valid(line) || !validUnicodeEscapes(line) {
		return r, ErrInput
	}
	// Reject duplicate keys, nulls, absent fields and unknown keys before decoding.
	dec := json.NewDecoder(bytes.NewReader(line))
	token, e := dec.Token()
	if e != nil || token != json.Delim('{') {
		return r, ErrInput
	}
	seen := map[string]bool{}
	allowed := map[string]bool{"dataset_id": true, "audio_fingerprint": true, "transcription_attempt_id": true, "channel": true, "tgid": true, "duration_ms": true, "raw_model_transcript": true, "human_reference_transcript": true, "model": true, "split": true, "reviewed_at": true}
	for dec.More() {
		k, e := dec.Token()
		if e != nil {
			return r, ErrInput
		}
		key, ok := k.(string)
		if !ok || seen[key] || !allowed[key] {
			return r, ErrInput
		}
		seen[key] = true
		var raw json.RawMessage
		if dec.Decode(&raw) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return r, ErrInput
		}
	}
	if _, e = dec.Token(); e != nil {
		return r, ErrInput
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return r, ErrInput
	}
	if len(seen) != 11 {
		return r, ErrInput
	}
	dec = json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if dec.Decode(&r) != nil {
		return r, ErrInput
	}
	sum := md5.Sum([]byte(r.Fingerprint)) // Export ID, not an integrity/security hash.
	channels := map[int64]string{57201: "CH1A", 57202: "CH2B", 57203: "CH3B", 57204: "CH4C"}
	if !fingerprintPattern.MatchString(r.Fingerprint) || r.DatasetID != "gfr-audio-v1:"+hex.EncodeToString(sum[:]) || r.AttemptID < 1 || r.DurationMS < 0 || channels[r.TGID] == "" || channels[r.TGID] != r.Channel || !modelPattern.MatchString(r.Model) || r.ReviewedAt.IsZero() || !validText(r.Raw) || !validText(r.Reference) || len(tokens(r.Reference)) == 0 {
		return r, ErrInput
	}
	if r.Split != "train" && r.Split != "validation" && r.Split != "test" {
		return r, ErrInput
	}
	return r, nil
}

// encoding/json otherwise silently replaces unpaired UTF-16 surrogate escapes.
// JSON syntax is already validated; skip escaped backslashes and require pairs.
func validUnicodeEscapes(line []byte) bool {
	for i := 0; i < len(line); i++ {
		if line[i] != '\\' {
			continue
		}
		i++
		if line[i] != 'u' {
			continue
		}
		n, e := strconv.ParseUint(string(line[i+1:i+5]), 16, 16)
		if e != nil {
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
			low, e := strconv.ParseUint(string(line[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

func evaluate(reader io.Reader) (Report, error) {
	report := Report{Version: MetricVersion, Vocabulary: "greenwich-critical-terms-v1", Overall: newCounts(), Splits: map[string]*Counts{}, PromotionReadiness: "not assessed; descriptive offline metrics only"}
	for _, s := range []string{"train", "validation", "test"} {
		c := newCounts()
		report.Splits[s] = &c
	}
	hash := sha256.New()
	limited := &io.LimitedReader{R: reader, N: MaxFileBytes + 1}
	scan := bufio.NewScanner(io.TeeReader(limited, hash))
	scan.Buffer(make([]byte, 4096), MaxLineBytes+2)
	ids, fps := map[string]bool{}, map[string]bool{}
	cells := 0
	for scan.Scan() {
		line := scan.Bytes()
		if len(line) > MaxLineBytes || report.Overall.Records >= MaxRecords {
			return Report{}, ErrInput
		}
		r, e := parse(line)
		if e != nil {
			return Report{}, fmt.Errorf("dataset line %d: %w", report.Overall.Records+1, ErrInput)
		}
		if ids[r.DatasetID] || fps[r.Fingerprint] {
			return Report{}, ErrInput
		}
		ids[r.DatasetID] = true
		fps[r.Fingerprint] = true
		ref, hyp := tokens(r.Reference), tokens(r.Raw)
		if len(ref) > MaxTokens || len(hyp) > MaxTokens {
			return Report{}, ErrInput
		}
		cells += len(ref) * len(hyp)
		if cells > MaxCells {
			return Report{}, ErrInput
		}
		ecount := distance(ref, hyp)
		report.Overall.add(ref, hyp, ecount)
		report.Splits[r.Split].add(ref, hyp, ecount)
	}
	if scan.Err() != nil || limited.N == 0 || report.Overall.Records == 0 {
		return Report{}, ErrInput
	}
	report.DatasetSHA256 = hex.EncodeToString(hash.Sum(nil))
	report.Overall.finish()
	for _, c := range report.Splits {
		c.finish()
	}
	report.Validation = "available; no promotion decision"
	if report.Splits["validation"].Records == 0 {
		report.Validation = "unavailable: zero validation records"
	}
	return report, nil
}

// EvaluateFile never writes. Every ancestor must be a real local directory;
// an anchored handle and before/after identity checks defend against replacement.
func EvaluateFile(path string) (Report, error) {
	fail := func() (Report, error) { return Report{}, ErrInput }
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.HasPrefix(path, `\\`) || strings.ContainsAny(path, "\x00\r\n") || strings.Contains(strings.TrimPrefix(path, filepath.VolumeName(path)), ":") {
		return fail()
	}
	ancestors := map[string]os.FileInfo{}
	for p := filepath.Dir(path); ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e != nil || isLink(info) || !info.IsDir() {
			return fail()
		}
		ancestors[p] = info
		if p == filepath.Dir(p) {
			break
		}
	}
	root, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		return fail()
	}
	defer root.Close()
	parent, e := root.Stat(".")
	if e != nil || !os.SameFile(parent, ancestors[filepath.Dir(path)]) {
		return fail()
	}
	name := filepath.Base(path)
	before, e := root.Lstat(name)
	if e != nil || isLink(before) || !before.Mode().IsRegular() || before.Size() > MaxFileBytes {
		return fail()
	}
	file, e := root.Open(name)
	if e != nil {
		return fail()
	}
	defer file.Close()
	opened, e := file.Stat()
	if e != nil || !os.SameFile(before, opened) {
		return fail()
	}
	report, e := evaluate(file)
	if e != nil {
		return Report{}, e
	}
	after, e := root.Lstat(name)
	if e != nil || isLink(after) || !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return fail()
	}
	for p, info := range ancestors {
		now, e := os.Lstat(p)
		if e != nil || isLink(now) || !os.SameFile(info, now) {
			return fail()
		}
	}
	return report, nil
}
