package unitrecognition

import (
	"reflect"
	"strings"
	"sync"
	"testing"
)

func matcher(t *testing.T) *Matcher {
	t.Helper()
	m, e := New()
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func expectedCatalog() []Unit {
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
func TestCatalogAndEveryPhrase(t *testing.T) {
	m := matcher(t)
	want := expectedCatalog()
	if !reflect.DeepEqual(m.Catalog(), want) || len(want) != 26 {
		t.Fatal("catalog mismatch")
	}
	career, volunteer := 0, 0
	for _, u := range want {
		if u.RosterKind == "career" {
			career++
		} else {
			volunteer++
			if u.Station != UnknownStation {
				t.Fatal("station")
			}
		}
		t.Run(u.CanonicalID, func(t *testing.T) {
			for _, s := range []string{u.Phrase, strings.ToUpper(u.Phrase)} {
				r := m.Recognize(s)
				if r.State != Resolved || r.Transcript != s || len(r.Mentions) != 1 || !r.Mentions[0].Accepted || r.Mentions[0].Unit != u || r.Mentions[0].Evidence != s || r.Mentions[0].Start != 0 || r.Mentions[0].End != len(s) {
					t.Fatalf("%+v", r)
				}
			}
		})
	}
	if career != 8 || volunteer != 18 {
		t.Fatal("counts")
	}
}

func TestExclusionsAndNumericBoundaries(t *testing.T) {
	m := matcher(t)
	for _, s := range []string{
		"MINI11", "E12", "E62", "E71", "E11", "TANKER2", "CAR5", "CAR1", "CAR4",
		"chief", "GEMS", "medic", "engine 20", "engine", "truck", "squad", "tanker", "ladder", "rescue",
		"engine 12", "engine 71", "engine 11", "tanker 2", "car 5", "car 1",
		"station 2", "unknown radio ID", "57201", "CH1A", "I-95", "Merritt Parkway",
		"engine two", "eng 2", "engine 2V", "engine2", "engine 2x", "xengine 2",
		"engine 2\u0301", "engine 2é", "éengine 2",
	} {
		if r := m.Recognize(s); r.State != Unresolved || len(r.Mentions) != 0 {
			t.Errorf("%q: %+v", s, r)
		}
	}
	for _, u := range expectedCatalog() {
		if r := m.Recognize(u.CanonicalID); r.State != Unresolved {
			t.Fatal("display code matched")
		}
	}
	for _, tc := range []struct{ s, id string }{
		{"engine 21", "E21V"}, {"engine 51", "E51V"}, {"tanker 17", "TK17V"}, {"rescue 51", "RESCUE51V"}, {"car 4", "DC"}, {"engine 62", "E62V"},
	} {
		r := m.Recognize(tc.s)
		if len(r.Mentions) != 1 || r.Mentions[0].CanonicalID != tc.id {
			t.Fatalf("%+v", r)
		}
	}
}

func TestPunctuationAndBarriers(t *testing.T) {
	m := matcher(t)
	for _, g := range []string{" ", "  ", "\t", ",", ".", ":", ";", "-", " ,\t"} {
		s := "engine" + g + "2"
		r := m.Recognize(s)
		if r.State != Resolved || len(r.Mentions) != 1 || r.Mentions[0].Evidence != s {
			t.Fatalf("gap %q: %+v", g, r)
		}
	}
	for _, s := range []string{"(engine 2)", " ( squad 8 ) ", "engine 2, engine 5", "engine 2. engine 5", "engine 2: engine 5", "engine 2; engine 5", "engine 2\nengine 5"} {
		if r := m.Recognize(s); r.State != Resolved {
			t.Fatalf("%q: %+v", s, r)
		}
	}
	for _, g := range []string{"\n", "\r", "\r\n", " + ", " / ", " unknown ", "(", ")", "_", "—", "\u00a0"} {
		if r := m.Recognize("engine" + g + "2"); r.State != Unresolved || len(r.Mentions) != 0 {
			t.Fatalf("barrier %q: %+v", g, r)
		}
	}
	for _, s := range []string{"(engine 2", "engine 2)", "(engine 2; engine 5)", "engine 2!", "engine 2 / engine 5"} {
		if r := m.Recognize(s); r.State != Unresolved {
			t.Fatalf("context %q: %+v", s, r)
		}
	}
}

func TestContextRejections(t *testing.T) {
	m := matcher(t)
	for _, s := range []string{
		"\"engine 2\"", "‘engine 2’", "not engine 2", "no engine 2",
		"yesterday engine 2", "engine 2 yesterday", "if engine 2", "engine 2 if needed",
		"we discussed engine 2", "engine 2 is training", "engine 2 on scene",
		"respond engine 2", "this is engine 2", "engine 2, not engine 5",
	} {
		r := m.Recognize(s)
		if r.State != Unresolved || len(r.Mentions) == 0 {
			t.Fatalf("%+v", r)
		}
		for _, v := range r.Mentions {
			if v.Accepted || v.Reason == "" || s[v.Start:v.End] != v.Evidence {
				t.Fatal("rejected evidence lost")
			}
		}
	}
	r := m.Recognize("yesterday engine 2\nengine 5")
	if r.State != Resolved || r.Mentions[0].Accepted || !r.Mentions[1].Accepted {
		t.Fatal("line-local policy")
	}
}

func TestRepeatedIndependentAndOffsets(t *testing.T) {
	m := matcher(t)
	s := "é 🚒\nENGINE-2; engine 5; engine 2"
	r := m.Recognize(s)
	if r.Transcript != s || r.State != Resolved || len(r.Mentions) != 3 {
		t.Fatalf("%+v", r)
	}
	for n, v := range r.Mentions {
		if !v.Accepted || s[v.Start:v.End] != v.Evidence || (n > 0 && v.Start <= r.Mentions[n-1].Start) {
			t.Fatal("offset/order")
		}
	}
	if r.Mentions[0].Start != len("é 🚒\n") || r.Mentions[0].CanonicalID != "E2" || r.Mentions[1].CanonicalID != "E5" {
		t.Fatal("identity/byte offset")
	}
}

func TestInvalidText(t *testing.T) {
	m := matcher(t)
	for _, bad := range []string{"\xff", "\x00", "\x0b", "\x0c", "\x7f", "\u0085", "\ufeff", "\u200b", "\u200d", "\u202e", "\u2066", "\u2028", "\u2029"} {
		s := "engine 2" + bad
		r := m.Recognize(s)
		if r.Transcript != s || r.State != Unresolved || r.Reason != "invalid_utf8_or_unsafe_controls" || len(r.Mentions) != 0 {
			t.Fatalf("%+v", r)
		}
	}
}

func TestInvalidCatalog(t *testing.T) {
	mutations := map[string]func([]Unit) []Unit{
		"short":                  func(u []Unit) []Unit { return u[:25] },
		"long":                   func(u []Unit) []Unit { return append(u, u[0]) },
		"duplicate ID":           func(u []Unit) []Unit { u[1].CanonicalID = u[0].CanonicalID; return u },
		"duplicate display":      func(u []Unit) []Unit { u[1].DisplayLabel = u[0].DisplayLabel; return u },
		"duplicate phrase":       func(u []Unit) []Unit { u[1].Phrase = u[0].Phrase; return u },
		"normalized case":        func(u []Unit) []Unit { u[1].Phrase = strings.ToUpper(u[0].Phrase); return u },
		"normalized punctuation": func(u []Unit) []Unit { u[1].Phrase = "car-4"; return u },
		"normalized spacing":     func(u []Unit) []Unit { u[1].Phrase = "car  4"; return u },
		"roster kind":            func(u []Unit) []Unit { u[0].RosterKind = "other"; return u },
		"class":                  func(u []Unit) []Unit { u[0].ApparatusClass = "other"; return u },
		"missing station":        func(u []Unit) []Unit { u[0].Station = ""; return u },
		"wrong station":          func(u []Unit) []Unit { u[0].Station = "STN8"; return u },
		"volunteer station":      func(u []Unit) []Unit { u[8].Station = "STN2"; return u },
		"career order":           func(u []Unit) []Unit { u[0], u[1] = u[1], u[0]; return u },
		"excluded ID":            func(u []Unit) []Unit { u[0].CanonicalID = "CAR4"; u[0].DisplayLabel = "CAR4"; return u },
		"excluded phrase":        func(u []Unit) []Unit { u[0].Phrase = "chief"; return u },
		"empty phrase":           func(u []Unit) []Unit { u[0].Phrase = ""; return u },
		"empty ID":               func(u []Unit) []Unit { u[0].CanonicalID = ""; return u },
		"empty display":          func(u []Unit) []Unit { u[0].DisplayLabel = ""; return u },
		"invalid UTF8":           func(u []Unit) []Unit { u[0].Phrase = "\xff"; return u },
		"wrong mapping":          func(u []Unit) []Unit { u[0].Phrase = "engine 4"; return u },
	}
	for name, f := range mutations {
		t.Run(name, func(t *testing.T) {
			if m, e := build(f(expectedCatalog())); e == nil || m != nil {
				t.Fatal("invalid catalog accepted")
			}
		})
	}
}

func TestAmbiguityAndLongestSeams(t *testing.T) {
	// Impossible via validated New: synthetic internal seam only.
	m := &Matcher{}
	a := Unit{CanonicalID: "A", Phrase: "fixture"}
	b := Unit{CanonicalID: "B", Phrase: "fixture longer"}
	m.insert(a)
	m.insert(b)
	r := m.Recognize("fixture longer")
	if len(r.Mentions) != 1 || r.Mentions[0].CanonicalID != "B" {
		t.Fatal("longest")
	}
	b.Phrase = "fixture"
	m.insert(b)
	r = m.Recognize("fixture")
	if r.State != Ambiguous || r.Reason != "conflicting_identities" || len(r.Mentions) != 1 ||
		r.Mentions[0].Accepted || r.Mentions[0].CanonicalID != "" || len(r.Mentions[0].Alternatives) != 2 {
		t.Fatalf("%+v", r)
	}
	r.Mentions[0].Alternatives[0].CanonicalID = "changed"
	if m.Recognize("fixture").Mentions[0].Alternatives[0].CanonicalID != "A" {
		t.Fatal("mutable conflict alternatives")
	}
}

func TestOwnershipIsolationAndThousandConcurrentCalls(t *testing.T) {
	m := matcher(t)
	catalog := m.Catalog()
	catalog[0].Phrase = "changed"
	source := expectedCatalog()
	other, e := build(source)
	if e != nil {
		t.Fatal(e)
	}
	source[0].Phrase = "changed"
	if other.Catalog()[0].Phrase != "car 4" || m.Catalog()[0].Phrase != "car 4" {
		t.Fatal("catalog alias")
	}
	texts := []string{"engine 2; engine 5; engine 2", "(squad 8)", "not engine 2", "engine", "2", "", "engine 62", "car 4", "\xff"}
	wants := make([]Result, len(texts))
	for n, s := range texts {
		wants[n] = m.Recognize(s)
	}
	for n := 0; n < 20; n++ {
		for k, s := range texts {
			if !reflect.DeepEqual(wants[k], m.Recognize(s)) {
				t.Fatal("state/determinism")
			}
		}
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for n := 0; n < 1000; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			k := n % len(texts)
			r := m.Recognize(texts[k])
			if !reflect.DeepEqual(r, wants[k]) {
				t.Error("concurrent mismatch")
			}
			if len(r.Mentions) > 0 {
				r.Mentions[0].CanonicalID = "mutated"
				r.Mentions[0].Reason = "mutated"
			}
		}(n)
	}
	close(start)
	wg.Wait()
	for k, s := range texts {
		if !reflect.DeepEqual(wants[k], m.Recognize(s)) {
			t.Fatal("mutation leak")
		}
	}
}
