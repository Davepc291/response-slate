package addresscandidate

import (
	"reflect"
	"strings"
	"testing"
)

func extractor(t *testing.T) *Extractor {
	t.Helper()
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestConfirmedAddresses(t *testing.T) {
	e := extractor(t)
	for _, tc := range []struct{ text, number, street string }{
		{"93 Doubling Road", "93", "doubling road"},
		{"30 Edgewood Drive", "30", "edgewood drive"},
		{"65 Hunting Ridge Road", "65", "hunting ridge road"},
		{"328 Pemberwick Road", "328", "pemberwick road"},
		{"534 Riversville Road", "534", "riversville road"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			got := e.Extract(tc.text)
			want := []Candidate{{tc.number, tc.street, Street, tc.text, 0, len(tc.text)}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestCasePunctuationAndByteOffsets(t *testing.T) {
	e := extractor(t)
	text := "Équipe: respond to (93, \"DOUBLING-Road\"), then 30 edgewood drive."
	original := text
	got := e.Extract(text)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	for i, phrase := range []string{"93, \"DOUBLING-Road", "30 edgewood drive"} {
		start := strings.Index(text, phrase)
		if got[i].Start != start || got[i].End != start+len(phrase) || got[i].Evidence != phrase || text[got[i].Start:got[i].End] != phrase {
			t.Fatalf("bad evidence %+v", got[i])
		}
	}
	if text != original || !reflect.DeepEqual(got, e.Extract(text)) {
		t.Fatal("input changed or nondeterministic offsets")
	}
}

func TestLongestMatch(t *testing.T) {
	e := extractor(t)
	got := e.Extract("12 Edgewood Avenue Connector and 13 Edgewood Avenue")
	if len(got) != 2 || got[0].CanonicalStreet != "edgewood avenue connector" || got[1].CanonicalStreet != "edgewood avenue" {
		t.Fatalf("longest match failed: %+v", got)
	}
	// A tie after punctuation normalization must not pick a name arbitrarily.
	e = &Extractor{}
	insert(&e.streets, "alpha road", Street)
	insert(&e.streets, "alpha-road", Street)
	if got = e.Extract("12 alpha road"); len(got) != 0 {
		t.Fatal("ambiguous dictionary match")
	}
}

func TestMultipleMentionsRemainSeparate(t *testing.T) {
	e := extractor(t)
	text := "93 Doubling Road; 30 Edgewood Drive; 93 Doubling Road"
	got := e.Extract(text)
	if len(got) != 3 || got[0].HouseNumber != "93" || got[1].HouseNumber != "30" || got[2].HouseNumber != "93" {
		t.Fatalf("lost mentions %+v", got)
	}
	for i, c := range got {
		if text[c.Start:c.End] != c.Evidence || (i > 0 && c.Start <= got[i-1].End) {
			t.Fatal("unordered/overlapping evidence")
		}
	}
}

func TestUnsupportedText(t *testing.T) {
	e := extractor(t)
	for _, text := range []string{
		"", "Doubling Road", "93", "93 Unknown Example Road", "93 Doubling Rd", "93 Doublng Road",
		"93 NotDoubling Road", "93 Doubling Roadway", "x93 Doubling Road", "93A Doubling Road", "93Doubling Road",
		"ninety three Doubling Road", "93 near Doubling Road", "93 at Doubling Road", "93.5 Doubling Road",
		"1,093 Doubling Road", "90-93 Doubling Road", "90 / 93 Doubling Road", "93 1/2 Doubling Road", "12 93 Doubling Road",
		"-93 Doubling Road", "+93 Doubling Road", "９３ Doubling Road", "93/Doubling Road",
		"93. Doubling Road", "93\nDoubling Road", "93 Doubling\nRoad", "93 @ Doubling Road",
		"93 Doubling Roa\u0301d", "93 Doubling Road\xff", "93 Doubling\x00 Road", "93 Doubling\u200b Road",
	} {
		t.Run(text, func(t *testing.T) {
			if got := e.Extract(text); len(got) != 0 {
				t.Fatalf("invented candidate: %+v", got)
			}
		})
	}
}

func TestNoCrossTransmissionState(t *testing.T) {
	e := extractor(t)
	for _, text := range []string{"93", "Doubling Road", "30", "Edgewood Drive"} {
		if len(e.Extract(text)) != 0 {
			t.Fatal("combined transmissions")
		}
	}
	if len(e.Extract("93 Doubling Road")) != 1 {
		t.Fatal("complete transmission missing")
	}
	if len(e.Extract("Doubling Road")) != 0 {
		t.Fatal("reused previous number")
	}
}

func TestAccessRoads(t *testing.T) {
	e := extractor(t)
	for _, phrase := range []string{"55 North St Driveway", "55 North Turning Loop Extension"} {
		got := e.Extract("Respond to " + phrase + ".")
		if len(got) != 1 || got[0].HouseNumber != "55" || got[0].CanonicalStreet != strings.ToLower(phrase) || got[0].DictionaryKind != AccessRoad || got[0].Evidence != phrase || got[0].Start != 11 || got[0].End != 11+len(phrase) {
			t.Fatalf("bad special match %+v", got)
		}
	}
	for _, text := range []string{"North St Driveway", "93 North St Driveway", "55 North Turning Loop", "12 55 North St Driveway"} {
		if len(e.Extract(text)) != 0 {
			t.Fatal("invented access-road candidate")
		}
	}
	got := e.Extract("55 North Street")
	if len(got) != 1 || got[0].DictionaryKind != Street || got[0].CanonicalStreet != "north street" {
		t.Fatal("access roads conflated with canonical street")
	}
}
