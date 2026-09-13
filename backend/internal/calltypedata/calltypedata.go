// Package calltypedata provides approved v1 vocabulary data only.
// It does not recognize transcripts or integrate with production systems.
package calltypedata

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	Version             = "call-type-vocabulary-v1"
	CallTypeCount       = 12
	AlarmLevelCount     = 4
	AlarmQualifierCount = 2
	PhraseCount         = 42
)

// Kind keeps call types, alarm levels, and alarm qualifiers distinct.
type Kind string

const (
	CallType       Kind = "call_type"
	AlarmLevel     Kind = "alarm_level"
	AlarmQualifier Kind = "alarm_qualifier"
)

// Phrase is approved source text and metadata, not a recognition result.
// RequiresDispatchContext is true for all call-type phrases. False for a level
// or qualifier does not authorize standalone recognition or call-type inference.
type Phrase struct {
	Exact                   string
	Kind                    Kind
	CanonicalValue          string
	RequiresDispatchContext bool
}

// Catalog is immutable through its exported API and safe for concurrent reads.
// The zero value is empty; use Load for a validated v1 catalog.
type Catalog struct {
	values  map[Kind][]string
	phrases []Phrase
	exact   map[string]Phrase
}

// Load validates the built-in catalog and returns no partial data on failure.
func Load() (*Catalog, error) {
	return newCatalog(map[Kind][]string{
		CallType:       {"ALARM RESD", "ALARM COMM", "INVEST CO-YES", "INVEST CO-NO", "HAZARD GAS", "WIRES", "SERVICE WATER", "SERVICE STRUCTURAL", "ELEVATOR RESCUE", "MVA INJURIES", "INVESTIGATE INSIDE", "INVESTIGATE OUTSIDE"},
		AlarmLevel:     {"STILL", "MINOR", "BOX", "WORKING FIRE"},
		AlarmQualifier: {"general fire activation", "smoke activation"},
	}, []Phrase{
		{"still alarm", AlarmLevel, "STILL", false},
		{"minor alarm", AlarmLevel, "MINOR", false},
		{"box alarm", AlarmLevel, "BOX", false},
		{"working fire", AlarmLevel, "WORKING FIRE", false},
		{"respond residential alarm", CallType, "ALARM RESD", true},
		{"respond to residential alarm", CallType, "ALARM RESD", true},
		{"respond to a residential alarm", CallType, "ALARM RESD", true},
		{"respond commercial alarm", CallType, "ALARM COMM", true},
		{"respond to commercial alarm", CallType, "ALARM COMM", true},
		{"respond to a commercial alarm", CallType, "ALARM COMM", true},
		{"general fire activation", AlarmQualifier, "general fire activation", false},
		{"smoke activation", AlarmQualifier, "smoke activation", false},
		{"CO alarm with symptoms", CallType, "INVEST CO-YES", true},
		{"carbon monoxide alarm with symptoms", CallType, "INVEST CO-YES", true},
		{"CO alarm without symptoms", CallType, "INVEST CO-NO", true},
		{"CO alarm no symptoms", CallType, "INVEST CO-NO", true},
		{"carbon monoxide alarm without symptoms", CallType, "INVEST CO-NO", true},
		{"carbon monoxide alarm no symptoms", CallType, "INVEST CO-NO", true},
		{"gas leak", CallType, "HAZARD GAS", true},
		{"natural gas leak", CallType, "HAZARD GAS", true},
		{"gas odor", CallType, "HAZARD GAS", true},
		{"odor of gas", CallType, "HAZARD GAS", true},
		{"wires down", CallType, "WIRES", true},
		{"wire down", CallType, "WIRES", true},
		{"wires arcing", CallType, "WIRES", true},
		{"wires burning", CallType, "WIRES", true},
		{"water problem", CallType, "SERVICE WATER", true},
		{"water leak", CallType, "SERVICE WATER", true},
		{"broken water pipe", CallType, "SERVICE WATER", true},
		{"structural problem", CallType, "SERVICE STRUCTURAL", true},
		{"structural damage", CallType, "SERVICE STRUCTURAL", true},
		{"elevator rescue", CallType, "ELEVATOR RESCUE", true},
		{"elevator entrapment", CallType, "ELEVATOR RESCUE", true},
		{"person trapped in an elevator", CallType, "ELEVATOR RESCUE", true},
		{"motor vehicle accident with injuries", CallType, "MVA INJURIES", true},
		{"motor vehicle accident, injuries", CallType, "MVA INJURIES", true},
		{"MVA with injuries", CallType, "MVA INJURIES", true},
		{"car accident with injuries", CallType, "MVA INJURIES", true},
		{"investigate inside", CallType, "INVESTIGATE INSIDE", true},
		{"investigation inside", CallType, "INVESTIGATE INSIDE", true},
		{"investigate outside", CallType, "INVESTIGATE OUTSIDE", true},
		{"investigation outside", CallType, "INVESTIGATE OUTSIDE", true},
	})
}

// Values returns an independent copy in approved catalog order.
// An unknown kind returns an empty collection.
func (c *Catalog) Values(kind Kind) []string { return slices.Clone(c.values[kind]) }

// Phrases returns an independent copy in Step 5B1 catalog order.
func (c *Catalog) Phrases() []Phrase { return slices.Clone(c.phrases) }

// Lookup is case-sensitive exact lookup. It never normalizes or scans input.
func (c *Catalog) Lookup(exact string) (Phrase, bool) {
	p, ok := c.exact[exact]
	return p, ok
}

func newCatalog(values map[Kind][]string, phrases []Phrase) (*Catalog, error) {
	expected := map[Kind]int{CallType: CallTypeCount, AlarmLevel: AlarmLevelCount, AlarmQualifier: AlarmQualifierCount}
	if len(values) != len(expected) || len(phrases) != PhraseCount {
		return nil, fmt.Errorf("calltypedata: invalid catalog counts")
	}
	c := &Catalog{values: make(map[Kind][]string), phrases: slices.Clone(phrases), exact: make(map[string]Phrase)}
	allowed := make(map[Kind]map[string]bool)
	for kind, entries := range values {
		count, ok := expected[kind]
		if !ok || len(entries) != count {
			return nil, fmt.Errorf("calltypedata: invalid concept or value count")
		}
		allowed[kind] = make(map[string]bool)
		normalized := make(map[string]bool)
		for _, value := range entries {
			key := validationKey(value)
			if !validText(value) || key == "" || allowed[kind][value] || normalized[key] {
				return nil, fmt.Errorf("calltypedata: invalid or duplicate canonical value")
			}
			allowed[kind][value] = true
			normalized[key] = true
		}
		c.values[kind] = slices.Clone(entries)
	}
	normalized := make(map[string]bool)
	covered := make(map[Kind]map[string]bool)
	counts := make(map[Kind]int)
	for _, p := range phrases {
		key := validationKey(p.Exact)
		if !validText(p.Exact) || key == "" || !allowed[p.Kind][p.CanonicalValue] {
			return nil, fmt.Errorf("calltypedata: invalid phrase concept or value")
		}
		if p.RequiresDispatchContext != (p.Kind == CallType) {
			return nil, fmt.Errorf("calltypedata: invalid dispatch-context flag")
		}
		if _, exists := c.exact[p.Exact]; exists {
			return nil, fmt.Errorf("calltypedata: duplicate exact phrase")
		}
		if normalized[key] {
			return nil, fmt.Errorf("calltypedata: duplicate normalized phrase")
		}
		normalized[key] = true
		c.exact[p.Exact] = p
		if covered[p.Kind] == nil {
			covered[p.Kind] = make(map[string]bool)
		}
		covered[p.Kind][p.CanonicalValue] = true
		counts[p.Kind]++
	}
	if counts[CallType] != 36 || counts[AlarmLevel] != 4 || counts[AlarmQualifier] != 2 {
		return nil, fmt.Errorf("calltypedata: invalid phrase counts by concept")
	}
	for kind, count := range expected {
		if len(covered[kind]) != count {
			return nil, fmt.Errorf("calltypedata: canonical value has no phrase")
		}
	}
	return c, nil
}

func validText(s string) bool {
	return s != "" && utf8.ValidString(s) && strings.TrimSpace(s) == s &&
		strings.IndexFunc(s, func(r rune) bool {
			return unicode.IsControl(r) || unicode.In(r, unicode.Cf) || r == '\u2028' || r == '\u2029'
		}) < 0
}

// validationKey detects catalog collisions only. Punctuation becomes spacing,
// spacing collapses, and case folds to lowercase. No public lookup uses this.
func validationKey(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) || unicode.IsSpace(r) {
			return ' '
		}
		return unicode.ToLower(r)
	}, s)), " ")
}
