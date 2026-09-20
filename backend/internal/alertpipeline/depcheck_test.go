package alertpipeline

// This test verifies, by static import inspection rather than by trusting a
// docstring, that this package and its alertstore persistence dependency
// never import anything that could translate detector output into incident,
// unit-status, or CAD authority, and never gain a network/notification
// capability. This mirrors ANE-01's "verified by dependency inspection"
// acceptance test from docs/alert-notification-engine-v1.md, extended to the
// new Step 8C packages.

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenImportSubstrings never legitimately appears in an import path of
// this pipeline or its persistence layer. Substring matching (not exact
// package names) so a future forbidden-package rename still gets caught.
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
	"internal/recordings", // never opens a recording file itself
	"internal/operations", // no shared live-server monitor coupling
	"internal/config",     // no GFR_* env var / live-server wiring
	"onesignal",
	"net/http",
	"net/smtp",
	"net/mail",
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
	for _, dir := range []string{".", "../alertstore"} {
		byFile := packageImports(t, dir)
		for file, imports := range byFile {
			for _, imp := range imports {
				for _, forbidden := range forbiddenImportSubstrings {
					if strings.Contains(imp, forbidden) {
						t.Fatalf("%s/%s imports %q, which matches forbidden substring %q: "+
							"this pipeline must never gain incident/unit-status/CAD authority, "+
							"a notification relay, or live-server wiring", dir, file, imp, forbidden)
					}
				}
			}
		}
	}
}

// TestNoCmdEntrypoint documents that Step 8C adds no new backend/cmd binary:
// the pipeline is dormant unless a test, or a future authorized integration,
// constructs and calls it explicitly. This is a repository-shape check, not
// a Go dependency check.
func TestNoCmdEntrypoint(t *testing.T) {
	cmdDir := filepath.Join("..", "..", "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.Contains(e.Name(), "alert") {
			t.Fatalf("unexpected backend/cmd/%s: Step 8C must not add a live entrypoint for this pipeline", e.Name())
		}
	}
}
