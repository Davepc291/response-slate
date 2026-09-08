// Package transcripteval evaluates private exports offline, without persistence.
package transcripteval

import (
	"embed"
	"strings"
	"unicode"
)

//go:embed vocabulary-v1.txt
var vocabulary embed.FS

const MetricVersion = "greenwich-evaluation-v1"

type Counts struct {
	Records        int        `json:"records"`
	Exact          int        `json:"exact_matches"`
	ExactRate      *float64   `json:"exact_match_rate"`
	Substitutions  int        `json:"substitutions"`
	Deletions      int        `json:"deletions"`
	Insertions     int        `json:"insertions"`
	ReferenceWords int        `json:"reference_words"`
	WER            *float64   `json:"word_error_rate"`
	Critical       []Term     `json:"critical_terms"`
	CriticalTotals TermTotals `json:"critical_totals"`
}
type TermTotals struct {
	Occurrences int      `json:"reference_occurrences"`
	Correct     int      `json:"correct_recognitions"`
	Misses      int      `json:"misses"`
	Recall      *float64 `json:"recall"`
}
type Term struct {
	Phrase      string   `json:"phrase"`
	Occurrences int      `json:"reference_occurrences"`
	Correct     int      `json:"correct_recognitions"`
	Misses      int      `json:"misses"`
	Recall      *float64 `json:"recall"`
}
type Report struct {
	Version            string             `json:"metric_version"`
	Vocabulary         string             `json:"vocabulary_version"`
	DatasetSHA256      string             `json:"dataset_sha256"`
	Overall            Counts             `json:"overall"`
	Splits             map[string]*Counts `json:"splits"`
	Validation         string             `json:"validation"`
	PromotionReadiness string             `json:"promotion_readiness"`
}

// Punctuation is a separator, not deleted: "10-4" becomes ["10", "4"].
func tokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
}

type edits struct{ s, d, i int }

func (e edits) cost() int { return e.s + e.d + e.i }

// Rolling-row Levenshtein; ties prefer diagonal, then deletion, then insertion.
func distance(ref, hyp []string) edits {
	prev := make([]edits, len(hyp)+1)
	next := make([]edits, len(hyp)+1)
	for j := range prev {
		prev[j].i = j
	}
	for i, r := range ref {
		next[0] = edits{d: i + 1}
		for j, h := range hyp {
			best := prev[j]
			if r != h {
				best.s++
			}
			del := prev[j+1]
			del.d++
			if del.cost() < best.cost() {
				best = del
			}
			ins := next[j]
			ins.i++
			if ins.cost() < best.cost() {
				best = ins
			}
			next[j+1] = best
		}
		prev, next = next, prev
	}
	return prev[len(hyp)]
}
func occurrences(words, phrase []string) int {
	n := 0
	for i := 0; i+len(phrase) <= len(words); i++ {
		match := true
		for j := range phrase {
			if words[i+j] != phrase[j] {
				match = false
				break
			}
		}
		if match {
			n++
		}
	}
	return n
}
func newCounts() Counts {
	b, _ := vocabulary.ReadFile("vocabulary-v1.txt")
	c := Counts{}
	for _, p := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		c.Critical = append(c.Critical, Term{Phrase: p})
	}
	return c
}
func (c *Counts) add(ref, hyp []string, e edits) {
	c.Records++
	if strings.Join(ref, " ") == strings.Join(hyp, " ") {
		c.Exact++
	}
	c.ReferenceWords += len(ref)
	c.Substitutions += e.s
	c.Deletions += e.d
	c.Insertions += e.i
	for i := range c.Critical {
		t := &c.Critical[i]
		p := tokens(t.Phrase)
		r, h := occurrences(ref, p), occurrences(hyp, p)
		t.Occurrences += r
		t.Correct += min(r, h)
		t.Misses += max(0, r-h)
	}
}
func ratio(a, b int) *float64 {
	if b == 0 {
		return nil
	}
	v := float64(a) / float64(b)
	return &v
}
func (c *Counts) finish() {
	c.ExactRate = ratio(c.Exact, c.Records)
	c.WER = ratio(c.Substitutions+c.Deletions+c.Insertions, c.ReferenceWords)
	c.CriticalTotals = TermTotals{}
	for i := range c.Critical {
		t := &c.Critical[i]
		t.Recall = ratio(t.Correct, t.Occurrences)
		c.CriticalTotals.Occurrences += t.Occurrences
		c.CriticalTotals.Correct += t.Correct
		c.CriticalTotals.Misses += t.Misses
	}
	c.CriticalTotals.Recall = ratio(c.CriticalTotals.Correct, c.CriticalTotals.Occurrences)
}
