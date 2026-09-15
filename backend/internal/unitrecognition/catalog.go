// Package unitrecognition recognizes offline unit mentions without operational authority.
package unitrecognition

import (
	"fmt"
	"slices"
	"strings"
)

const Version = "unit-vocabulary-v1"
const UnknownStation = "unknown"

// Unit contains approved metadata only; Phrase is the sole spoken input.
type Unit struct {
	CanonicalID    string
	DisplayLabel   string
	RosterKind     string
	ApparatusClass string
	Station        string
	Phrase         string
}

// approved returns fresh data in fixed career order, then volunteer catalog order.
func approved() []Unit {
	return []Unit{
		{"DC", "DC", "career", "command", "HQ", "car 4"},
		{"E2", "E2", "career", "engine", "STN2", "engine 2"},
		{"E3", "E3", "career", "engine", "STN3", "engine 3"},
		{"E4", "E4", "career", "engine", "STN4", "engine 4"},
		{"E5", "E5", "career", "engine", "STN5", "engine 5"},
		{"SQ1", "SQ1", "career", "squad", "HQ", "squad 1"},
		{"SQ8", "SQ8", "career", "squad", "STN8", "squad 8"},
		{"T1", "T1", "career", "truck", "HQ", "truck 1"},
		{"E21V", "E21V", "volunteer", "engine", "unknown", "engine 21"},
		{"E31V", "E31V", "volunteer", "engine", "unknown", "engine 31"},
		{"E41V", "E41V", "volunteer", "engine", "unknown", "engine 41"},
		{"E51V", "E51V", "volunteer", "engine", "unknown", "engine 51"},
		{"E62V", "E62V", "volunteer", "engine", "unknown", "engine 62"},
		{"L4V", "L4V", "volunteer", "ladder", "unknown", "ladder 4"},
		{"L5V", "L5V", "volunteer", "ladder", "unknown", "ladder 5"},
		{"SQ2V", "SQ2V", "volunteer", "squad", "unknown", "squad 2"},
		{"SQ3V", "SQ3V", "volunteer", "squad", "unknown", "squad 3"},
		{"SQ4V", "SQ4V", "volunteer", "squad", "unknown", "squad 4"},
		{"SQ5V", "SQ5V", "volunteer", "squad", "unknown", "squad 5"},
		{"SQ6V", "SQ6V", "volunteer", "squad", "unknown", "squad 6"},
		{"TK6V", "TK6V", "volunteer", "tanker", "unknown", "tanker 6"},
		{"TK7V", "TK7V", "volunteer", "tanker", "unknown", "tanker 7"},
		{"TK17V", "TK17V", "volunteer", "tanker", "unknown", "tanker 17"},
		{"RESCUE5", "RESCUE5", "volunteer", "rescue", "unknown", "rescue 5"},
		{"RESCUE51V", "RESCUE51V", "volunteer", "rescue", "unknown", "rescue 51"},
		{"RESCUE7", "RESCUE7", "volunteer", "rescue", "unknown", "rescue 7"},
	}
}

func validate(units []Unit) error {
	if len(units) != 26 {
		return fmt.Errorf("unitrecognition: expected 26 units")
	}
	ids, labels, phrases := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, u := range units {
		key := strings.ToLower(strings.Join(strings.FieldsFunc(u.Phrase, func(r rune) bool { return strings.ContainsRune(" \t,.;:-", r) }), " "))
		if u.CanonicalID == "" || u.DisplayLabel == "" || key == "" || ids[u.CanonicalID] || labels[u.DisplayLabel] || phrases[key] {
			return fmt.Errorf("unitrecognition: empty or duplicate identity, label, or normalized phrase")
		}
		ids[u.CanonicalID] = true
		labels[u.DisplayLabel] = true
		phrases[key] = true
	}
	// Exact v1 metadata/order also enforces kinds, classes, stations, exclusions,
	// and phrase assignments. This is not a prefix-based identity rule.
	if !slices.Equal(units, approved()) {
		return fmt.Errorf("unitrecognition: catalog differs from approved v1")
	}
	return nil
}

// Catalog returns an independent copy in approved order.
func (m *Matcher) Catalog() []Unit { return slices.Clone(m.units) }
