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
		{streetText, "909DBB45B763F29826F520126D89263461E8C74D8198BCA8CC0788258FCADB68"},
		{accessRoadText, "68DA81578887B4C37785401928477418B768C4DE246AD982868E567CACB83CCB"},
	} {
		if got := fmt.Sprintf("%X", sha256.Sum256([]byte(tc.text))); got != tc.hash {
			t.Fatalf("embedded source bytes changed: %s", got)
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
