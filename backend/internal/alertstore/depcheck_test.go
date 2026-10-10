package alertstore

// This test verifies, by static import inspection rather than by trusting a
// docstring, that this package never imports anything that could translate
// alert-event persistence into incident/unit-status/CAD authority, or gain
// a network/notification-delivery capability. Mirrors
// backend/internal/alerts/depcheck_test.go's own exact list, added in Step
// 8D-C Part 1 specifically because the design lock named this package as
// one that must never acquire a reverse dependency on the new
// backend/internal/alertnotify bridge.

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenImportSubstrings mirrors alerts/depcheck_test.go's own list
// exactly, substring-matched so a future forbidden-package rename still
// gets caught. internal/alerts itself is deliberately absent -- this
// package's own real, legitimate domain dependency.
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
	"notifyoutbox",
	"notifyoutboxstore",
	"notifyrelay",
	"notifywebpush",
	"notifyattempt",
	"notifypreferences",
	"notifydelivery",
	"notifydeliverystore",
	"notifyworker",
	"notifyoutcome",
	// Step 8D-C Part 1: this package must never acquire a reverse
	// dependency on the alert->outbox bridge, which depends on the pure
	// alerts.Event type it helps produce, never on this persistence layer
	// directly and never the other way.
	"alertnotify",
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

// TestNoCmdEntrypoint documents that this part adds no new backend/cmd
// binary for this package. This is a repository-shape check, not a Go
// dependency check.
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
			t.Fatalf("unexpected backend/cmd/%s: this part must not add a live entrypoint for this package", e.Name())
		}
	}
}
