package notifyoutcome

// This test verifies, by static import inspection rather than by trusting a
// docstring, that this package never imports anything that could translate
// atomic outcome-recording authority into incident/unit-status/CAD
// authority, a live HTTP route, a live radio/alert trigger, a provider SDK,
// or a direct dependency on a live radio/CAD/alert event source. Modeled
// directly on backend/internal/notifyworker/depcheck_test.go, added in Step
// 8D-B Part 14B.

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenImportSubstrings never legitimately appears in an import path of
// this package. Substring matching (not exact package names) so a future
// forbidden-package rename still gets caught.
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
	"internal/httpapi", // no HTTP route in this part
	"internal/recordings",
	"internal/operations", // no shared live-server monitor coupling
	"internal/config",     // no GFR_* env var / live-server wiring in this part
	// This package is an atomic persistence-composition layer only: it must
	// never itself read a live radio/CAD/alert event, and it must never gain
	// eligibility-evaluation or provider-sending authority -- those remain
	// backend/internal/notifypreferences/notifyattempt and
	// backend/internal/notifyrelay/notifywebpush's own jobs respectively,
	// neither of which this package needs or imports.
	"internal/alerts",
	"internal/alertpipeline",
	"internal/alertstore",
	"notifyattempt",
	"notifypreferences",
	"notifyrelay",
	"notifywebpush",
	"notifyworker", // this package is composed BY notifyworker, never the reverse
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
	byFile := packageImports(t, ".")
	for file, imports := range byFile {
		for _, imp := range imports {
			for _, forbidden := range forbiddenImportSubstrings {
				if strings.Contains(imp, forbidden) {
					t.Fatalf("%s imports %q, which matches forbidden substring %q: "+
						"this package must remain a pure persistence-composition layer, "+
						"never gaining eligibility-evaluation, provider-sending, or "+
						"live-event authority",
						file, imp, forbidden)
				}
			}
		}
	}
}

// TestNoCmdEntrypoint documents that this part adds no new backend/cmd
// binary: the coordinator is dormant unless a test, or notifyworker, calls
// it explicitly. This is a repository-shape check, not a Go dependency
// check.
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
		if e.Name() == "notifyoutcome" {
			t.Fatalf("unexpected backend/cmd/%s: this part must not add a live entrypoint for this package", e.Name())
		}
	}
}

// TestNotRegisteredByAPIServer documents that backend/cmd/api/main.go never
// imports this package in Part 14B.
func TestNotRegisteredByAPIServer(t *testing.T) {
	path := filepath.Join("..", "..", "cmd", "api", "main.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		if strings.Contains(p, "notifyoutcome") {
			t.Fatalf("cmd/api/main.go imports %q: this part must not register the outcome coordinator with the live server", p)
		}
	}
}
