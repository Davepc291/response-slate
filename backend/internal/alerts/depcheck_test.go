package alerts

// This test verifies, by static import inspection rather than by trusting a
// docstring, that this package never imports anything that could translate
// detector/alert-event output into incident/unit-status/CAD authority, or
// gain a network/notification-delivery capability. Modeled directly on
// backend/internal/alertpipeline/depcheck_test.go, added in Step 8D-B Part
// 14A because this package itself had no independent dependency-guard
// coverage before now: alertpipeline's own depcheck_test.go only scans "."
// (itself) and "../alertstore", never "../alerts".

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenImportSubstrings mirrors alertpipeline/depcheck_test.go's own
// list exactly, substring-matched so a future forbidden-package rename still
// gets caught.
var forbiddenImportSubstrings = []string{
	"internal/unitstatus",
	"internal/unitrecognition",
	"internal/addresspipeline",
	"internal/addressrole",
	"internal/addresscandidate",
	"internal/addressdata",
	"internal/calltypepipeline",
	"internal/calltypeinterpret",
	"internal/calltypecandidate",
	"internal/calltypedata",
	"internal/dispatchinterpretation",
	"internal/httpapi",
	"internal/recordings",
	"internal/operations",
	"internal/config",
	"onesignal",
	"net/http",
	"net/smtp",
	"net/mail",
	// Step 8D-B Part 14A: this package must never gain a direct wiring path
	// into notification delivery/orchestration (see
	// backend/internal/notifyworker's own AlertLookup pattern, which exists
	// specifically so the dependency only ever runs the other way).
	"notifyoutbox",
	"notifyoutboxstore",
	"notifyrelay",
	"notifywebpush",
	"notifyattempt",
	"notifypreferences",
	"notifydelivery",
	"notifydeliverystore",
	"notifyworker",
}

func packageImports(t *testing.T, dir string) map[string][]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		var imports []string
		for _, imp := range f.Imports {
			imports = append(imports, strings.Trim(imp.Path.Value, `"`))
		}
		out[e.Name()] = imports
	}
	return out
}

func TestNoForbiddenDependencies(t *testing.T) {
	byFile := packageImports(t, ".")
	for file, imports := range byFile {
		for _, imp := range imports {
			for _, forbidden := range forbiddenImportSubstrings {
				if strings.Contains(imp, forbidden) {
					t.Fatalf("%s imports %q, which matches forbidden substring %q: "+
						"this package must never gain incident/unit-status/CAD authority, "+
						"a notification relay/orchestration dependency, or live-server wiring",
						file, imp, forbidden)
				}
			}
		}
	}
}
