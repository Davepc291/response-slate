package addressdata

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestEmbeddedDictionary(t *testing.T) {
	d, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Streets()) != 1211 || len(d.AccessRoads()) != 2 {
		t.Fatal("incorrect counts")
	}
	for _, name := range []string{"doubling road", "dingletown road", "edgewood drive", "hunting ridge road", "pemberwick road", "riversville road", "quaker lane", "fairchild lane"} {
		if !d.HasStreet(name) || d.HasAccessRoad(name) {
			t.Errorf("confirmed street missing or in wrong set: %s", name)
		}
	}
	for _, name := range []string{"55 north st driveway", "55 north turning loop extension"} {
		if !d.HasAccessRoad(name) || d.HasStreet(name) {
			t.Errorf("access road missing or in wrong set: %s", name)
		}
	}
	for _, name := range []string{"Doubling Road", "doubling road ", " doubling road", "doubling rd", "doubling", "doublng road", ""} {
		if d.HasStreet(name) || d.HasAccessRoad(name) {
			t.Errorf("non-exact match accepted: %q", name)
		}
	}
	for _, name := range d.Streets() {
		if d.HasAccessRoad(name) {
			t.Fatal("sets overlap")
		}
	}
	streets, access := d.Streets(), d.AccessRoads()
	if !sort.StringsAreSorted(streets) || !sort.StringsAreSorted(access) {
		t.Fatal("unstable order")
	}
	streets[0], access[0] = "changed", "changed"
	if d.HasStreet("changed") || d.HasAccessRoad("changed") || d.Streets()[0] == "changed" || d.AccessRoads()[0] == "changed" {
		t.Fatal("mutable dictionary exposed")
	}
	again, err := Load()
	if err != nil || !reflect.DeepEqual(d.Streets(), again.Streets()) || !reflect.DeepEqual(d.AccessRoads(), again.AccessRoads()) {
		t.Fatal("nondeterministic load")
	}
}

func TestEmbeddedHashes(t *testing.T) {
	for _, tc := range []struct{ text, hash string }{
		{streetText, "991BEF76DA14A9763D23017912529002EF02468FB56DC437D07D8967352385D7"},
		{accessRoadText, "DEDB1B166F16D5AB36EC01808BB3B4796501C0D7812AC70701267BC1117145B4"},
	} {
		lf := strings.ReplaceAll(tc.text, "\r\n", "\n")
		crlf := strings.ReplaceAll(lf, "\n", "\r\n")
		for _, text := range []string{tc.text, lf, crlf} {
			if got := normalizedHash(text); got != tc.hash {
				t.Fatalf("normalized embedded content changed: %s", got)
			}
		}
		lines := strings.Split(lf, "\n")
		lines[0], lines[1] = lines[1], lines[0]
		for _, changed := range []string{
			"changed " + lf,           // Actual entry change, still lowercase text.
			strings.Join(lines, "\n"), // Same entries, changed source ordering.
			"\ufeff" + lf, " " + lf, lf + "\n", strings.TrimSuffix(lf, "\n"),
			strings.Replace(lf, "\n", "\r", 1), // Bare CR is not normalized away.
		} {
			if normalizedHash(changed) == tc.hash {
				t.Fatal("content/order change passed the pinned hash check")
			}
		}
	}
}

// Normalize checkout line endings only. Do not trim, sort, lowercase, or
// remove BOMs: all other bytes and source ordering remain protected by the pin.
func normalizedHash(text string) string {
	return fmt.Sprintf("%X", sha256.Sum256([]byte(strings.ReplaceAll(text, "\r\n", "\n"))))
}

func TestDictionaryLineEndingEquivalence(t *testing.T) {
	streets := strings.ReplaceAll(streetText, "\r\n", "\n")
	access := strings.ReplaceAll(accessRoadText, "\r\n", "\n")
	want, err := load(streets, access)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{streets, strings.ReplaceAll(streets, "\n", "\r\n")} {
		for _, a := range []string{access, strings.ReplaceAll(access, "\n", "\r\n")} {
			got, err := load(s, a)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want.Streets(), got.Streets()) || !reflect.DeepEqual(want.AccessRoads(), got.AccessRoads()) {
				t.Fatal("line endings changed dictionary entries or lookup ordering")
			}
		}
	}
}

func TestValidation(t *testing.T) {
	for name, text := range map[string]string{
		"invalid UTF8": "road\xff", "BOM": "\ufeffroad", "embedded BOM": "ro\ufeffad", "empty": "", "blank first": "\nroad", "blank middle": "road\n\nother", "blank final": "road\n\n",
		"leading space": " road", "trailing space": "road ", "unicode whitespace": "road\u00a0", "uppercase": "Road", "unicode uppercase": "école É", "titlecase": "ǅ road", "tab": "a\troad", "NUL": "a\x00road", "DEL": "a\x7froad", "bare CR": "road\r", "format control": "ro\u200bad", "line separator": "ro\u2028ad", "paragraph separator": "ro\u2029ad", "duplicate": "road\nroad",
	} {
		t.Run(name, func(t *testing.T) {
			if set, err := parseSet(text, "fixture"); err == nil || set != nil {
				t.Fatal("invalid set accepted")
			}
		})
	}
	for _, text := range []string{"road", "road\n", "road\r\n", "road\nother\n", "road\r\nother\r\n", "école road"} {
		if _, err := parseSet(text, "fixture"); err != nil {
			t.Fatalf("valid encoding rejected: %v", err)
		}
	}
}

func TestCountsAndCrossSetDuplicates(t *testing.T) {
	streets := strings.TrimSuffix(streetText, "\n")
	for _, tc := range []struct{ streets, access string }{
		{"road", accessRoadText}, {streetText, "road"}, {streetText, accessRoadText + "extra access\n"}, {streetText + "extra street\n", accessRoadText},
		{streetText, "doubling road\n55 north st driveway\n"},
		{streets + "\ndoubling road\n", accessRoadText},
		{streetText, "55 north st driveway\n55 north st driveway\n"},
		{"bad\xff", accessRoadText}, {streetText, "Bad road\nother road\n"},
	} {
		if d, err := load(tc.streets, tc.access); err == nil || d != nil {
			t.Fatal("invalid dictionary or partial result returned")
		}
	}
}
