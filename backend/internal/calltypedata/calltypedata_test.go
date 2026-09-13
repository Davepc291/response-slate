package calltypedata

import (
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Snapshot of Dave's exact Step 5B1 phrases, including uppercase CO/MVA and comma.
func approvedPhrases() []Phrase {
	return []Phrase{
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
	}
}

func mustLoad(t *testing.T) *Catalog {
	t.Helper()
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestApprovedCatalog(t *testing.T) {
	c := mustLoad(t)
	if Version != "call-type-vocabulary-v1" || len(c.Phrases()) != 42 {
		t.Fatal("incorrect version/count")
	}
	if !reflect.DeepEqual(c.Phrases(), approvedPhrases()) {
		t.Fatal("approved phrase records/order changed")
	}
	want := map[Kind][]string{
		CallType:       {"ALARM RESD", "ALARM COMM", "INVEST CO-YES", "INVEST CO-NO", "HAZARD GAS", "WIRES", "SERVICE WATER", "SERVICE STRUCTURAL", "ELEVATOR RESCUE", "MVA INJURIES", "INVESTIGATE INSIDE", "INVESTIGATE OUTSIDE"},
		AlarmLevel:     {"STILL", "MINOR", "BOX", "WORKING FIRE"},
		AlarmQualifier: {"general fire activation", "smoke activation"},
	}
	for kind, values := range want {
		if !reflect.DeepEqual(c.Values(kind), values) {
			t.Errorf("incorrect %s values/order", kind)
		}
	}
	if len(c.Values(Kind("unknown"))) != 0 {
		t.Fatal("unknown kind has values")
	}
	counts := map[Kind]int{}
	for _, p := range approvedPhrases() {
		got, ok := c.Lookup(p.Exact)
		if !ok || got != p {
			t.Errorf("lookup mismatch: %q", p.Exact)
		}
		if p.RequiresDispatchContext != (p.Kind == CallType) {
			t.Errorf("context flag: %q", p.Exact)
		}
		counts[p.Kind]++
	}
	if counts[CallType] != 36 || counts[AlarmLevel] != 4 || counts[AlarmQualifier] != 2 {
		t.Fatal("concept separation/counts")
	}
}

func TestExactLookupAndExclusions(t *testing.T) {
	c := mustLoad(t)
	for _, text := range []string{
		"", "unknown", "alarm", "wires", "CO alarm", "investigate", "investigation",
		"I-95", "Merritt Parkway", "residential alarm", "commercial alarm",
		"co alarm with symptoms", "mva with injuries", "Gas leak",
		"gas leak ", " gas leak", "gas  leak", "gas-leak", "gas leak.",
		"motor vehicle accident injuries", "respond to a gas leak",
	} {
		if _, ok := c.Lookup(text); ok {
			t.Errorf("non-exact/excluded phrase accepted: %q", text)
		}
	}
}

func TestReadOnlyAndDeterministic(t *testing.T) {
	c := mustLoad(t)
	want := c.Phrases()
	for kind := range c.values {
		before := c.Values(kind)
		changed := c.Values(kind)
		changed[0] = "changed"
		if !reflect.DeepEqual(c.Values(kind), before) {
			t.Fatal("mutable values exposed")
		}
	}
	changed := c.Phrases()
	changed[0] = Phrase{}
	p, _ := c.Lookup(want[0].Exact)
	p.Exact = "changed"
	if !reflect.DeepEqual(c.Phrases(), want) {
		t.Fatal("mutable phrases exposed")
	}
	for i := 0; i < 10; i++ {
		if !reflect.DeepEqual(mustLoad(t).Phrases(), want) {
			t.Fatal("nondeterministic load")
		}
	}
	// Read-only access can be shared; no transcript state exists.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, expected := range want {
				if got, ok := c.Lookup(expected.Exact); !ok || got != expected {
					t.Error("concurrent lookup mismatch")
				}
			}
		}()
	}
	wg.Wait()
}

func TestConstructionCopiesInputs(t *testing.T) {
	source := mustLoad(t)
	values := map[Kind][]string{}
	for kind := range source.values {
		values[kind] = source.Values(kind)
	}
	phrases := source.Phrases()
	c, err := newCatalog(values, phrases)
	if err != nil {
		t.Fatal(err)
	}
	values[CallType][0] = "changed"
	delete(values, AlarmLevel)
	phrases[0] = Phrase{}
	if !reflect.DeepEqual(c.Phrases(), source.Phrases()) ||
		!reflect.DeepEqual(c.Values(CallType), source.Values(CallType)) ||
		!reflect.DeepEqual(c.Values(AlarmLevel), source.Values(AlarmLevel)) {
		t.Fatal("constructor retained caller-owned collections")
	}
}

func TestInvalidCatalog(t *testing.T) {
	type mutation func(map[Kind][]string, []Phrase) (map[Kind][]string, []Phrase)
	tests := map[string]mutation{
		"empty phrase":     func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) { p[0].Exact = ""; return v, p },
		"blank phrase":     func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) { p[0].Exact = " "; return v, p },
		"punctuation only": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) { p[0].Exact = "..."; return v, p },
		"invalid UTF8": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			p[0].Exact = "bad\xff"
			return v, p
		},
		"control": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			p[0].Exact = "bad\x00"
			return v, p
		},
		"BOM": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			p[0].Exact = "\ufeffstill alarm"
			return v, p
		},
		"empty mapped value": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			p[0].CanonicalValue = ""
			return v, p
		},
		"unknown value": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			p[0].CanonicalValue = "OTHER"
			return v, p
		},
		"wrong concept value": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			p[0].CanonicalValue = "WIRES"
			return v, p
		},
		"invalid kind": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			p[0].Kind = "invalid"
			return v, p
		},
		"duplicate exact": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) { p[1] = p[0]; return v, p },
		"multiple values": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			p[1].Exact = p[0].Exact
			return v, p
		},
		"multiple concepts": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			p[4].Exact = p[0].Exact
			return v, p
		},
		"normalized case": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			p[1].Exact = strings.ToUpper(p[0].Exact)
			return v, p
		},
		"normalized spacing": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			p[1].Exact = "still  alarm"
			return v, p
		},
		"normalized punctuation": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			p[1].Exact = "still, alarm"
			return v, p
		},
		"missing phrase": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) { return v, p[:41] },
		"extra phrase":   func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) { return v, append(p, p[0]) },
		"missing set": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			delete(v, AlarmLevel)
			return v, p
		},
		"invalid set": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			v["bad"] = v[AlarmLevel]
			delete(v, AlarmLevel)
			return v, p
		},
		"empty canonical": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) { v[CallType][0] = ""; return v, p },
		"duplicate canonical": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			v[CallType][1] = v[CallType][0]
			return v, p
		},
		"normalized canonical": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			v[CallType][1] = strings.ToLower(v[CallType][0])
			return v, p
		},
		"uncovered value": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			p[0].CanonicalValue = "MINOR"
			return v, p
		},
		"call context missing": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			p[4].RequiresDispatchContext = false
			return v, p
		},
		"level context altered": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			p[0].RequiresDispatchContext = true
			return v, p
		},
		"phrase concept counts": func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			p[4].Kind = AlarmLevel
			p[4].CanonicalValue = "STILL"
			p[4].RequiresDispatchContext = false
			return v, p
		},
	}
	for _, kind := range []Kind{CallType, AlarmLevel, AlarmQualifier} {
		tests["missing value "+string(kind)] = func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			v[kind] = v[kind][1:]
			return v, p
		}
		tests["extra value "+string(kind)] = func(v map[Kind][]string, p []Phrase) (map[Kind][]string, []Phrase) {
			v[kind] = append(v[kind], "EXTRA")
			return v, p
		}
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			source := mustLoad(t)
			values, phrases := mutate(source.values, source.Phrases())
			if c, err := newCatalog(values, phrases); err == nil || c != nil {
				t.Fatal("invalid catalog or partial data accepted")
			}
		})
	}
}
