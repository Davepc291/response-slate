// Package addressdata provides the versioned Greenwich dictionary without
// address extraction, correction, or production integration.
package addressdata

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	Version         = "greenwich-addresses-v1"
	StreetCount     = 1211
	AccessRoadCount = 2
)

//go:embed greenwich-streets-v1.txt
var streetText string

//go:embed greenwich-access-roads-v1.txt
var accessRoadText string

// Dictionary keeps canonical streets and special access roads separate.
// Its contents are immutable through the exported API and safe for concurrent reads.
type Dictionary struct {
	streets     map[string]struct{}
	accessRoads map[string]struct{}
}

// Load validates both embedded sets. Invalid data yields no partial dictionary.
func Load() (*Dictionary, error) {
	return load(streetText, accessRoadText)
}

func load(streets, accessRoads string) (*Dictionary, error) {
	s, err := parseSet(streets, "streets")
	if err != nil {
		return nil, err
	}
	a, err := parseSet(accessRoads, "access roads")
	if err != nil {
		return nil, err
	}
	for entry := range a {
		if _, exists := s[entry]; exists {
			return nil, fmt.Errorf("addressdata: duplicate across sets")
		}
	}
	if len(s) != StreetCount || len(a) != AccessRoadCount {
		return nil, fmt.Errorf("addressdata: expected %d streets and %d access roads, got %d and %d", StreetCount, AccessRoadCount, len(s), len(a))
	}
	return &Dictionary{streets: s, accessRoads: a}, nil
}

func parseSet(text, label string) (map[string]struct{}, error) {
	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("addressdata: %s is not UTF-8", label)
	}
	// Accept LF or CRLF and one optional final line ending, never trim entries.
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	entries := make(map[string]struct{})
	for i, entry := range strings.Split(text, "\n") {
		if entry == "" || strings.TrimSpace(entry) != entry || strings.IndexFunc(entry, func(r rune) bool {
			return unicode.IsUpper(r) || unicode.IsTitle(r) || unicode.IsControl(r) || unicode.In(r, unicode.Cf) || r == '\u2028' || r == '\u2029'
		}) >= 0 {
			return nil, fmt.Errorf("addressdata: invalid %s entry at line %d", label, i+1)
		}
		if _, exists := entries[entry]; exists {
			return nil, fmt.Errorf("addressdata: duplicate %s entry at line %d", label, i+1)
		}
		entries[entry] = struct{}{}
	}
	return entries, nil
}

// HasStreet performs a case-sensitive exact match against canonical streets.
// It does not trim, lowercase, expand abbreviations, or correct the input.
func (d *Dictionary) HasStreet(name string) bool { _, ok := d.streets[name]; return ok }

// HasAccessRoad performs the same exact lookup against only special access roads.
func (d *Dictionary) HasAccessRoad(name string) bool { _, ok := d.accessRoads[name]; return ok }

// Streets returns an independent, lexicographically sorted copy.
func (d *Dictionary) Streets() []string { return sorted(d.streets) }

// AccessRoads returns an independent, lexicographically sorted copy.
func (d *Dictionary) AccessRoads() []string { return sorted(d.accessRoads) }

func sorted(set map[string]struct{}) []string {
	entries := make([]string, 0, len(set))
	for entry := range set {
		entries = append(entries, entry)
	}
	sort.Strings(entries)
	return entries
}
