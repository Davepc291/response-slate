package notifydevices

// This test verifies, by static import inspection rather than by trusting a
// docstring, that this package never imports anything that could translate
// device registration into incident/unit-status/CAD authority, a live HTTP
// route, a live radio/alert trigger, a provider SDK, or any network
// capability. Modeled directly on
// backend/internal/alertpipeline/depcheck_test.go, extended for this
// package's own scope (Step 8D-B's first substep,
// docs/notification-relay-amendment-v1.md Section 25.1).

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
	"internal/httpapi",    // no HTTP route in this substep
	"internal/recordings", // no live radio/alert trigger wiring
	"internal/operations", // no shared live-server monitor coupling
	"internal/config",     // no GFR_* env var / live-server wiring yet
	// This substep is device registration only: it must not itself consume
	// or trigger from alert events (that is the future notifyoutbox
	// package's job, gated on its own separate authorization).
	"internal/alerts",
	"internal/alertpipeline",
	"internal/alertstore",
	"onesignal",
	"net/http",
	"net/smtp",
	"net/mail",
	// This substep is in-memory only, with no database migration yet: a
	// database import here would mean persistence quietly arrived without
	// the schema/migration review that requires.
	"database/sql",
	"internal/database",
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
						"a live HTTP route, a live radio/alert trigger, or a provider/network capability",
						file, imp, forbidden)
				}
			}
		}
	}
}

// TestNoCmdEntrypoint documents that this substep adds no new backend/cmd
// binary: the package is dormant unless a test, or a future authorized
// integration, constructs and calls it explicitly. This is a
// repository-shape check, not a Go dependency check.
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
		if strings.Contains(e.Name(), "notify") {
			t.Fatalf("unexpected backend/cmd/%s: this substep must not add a live entrypoint for this package", e.Name())
		}
	}
}

// TestNoEnvironmentVariables documents that this substep reads no GFR_*
// environment variable: it is a pure in-memory domain package, and
// environment-variable wiring (GFR_NOTIFY_*, per
// docs/notification-relay-amendment-v1.md Section 25.5) is explicitly out of
// scope until a later, separately authorized substep.
func TestNoEnvironmentVariables(t *testing.T) {
	byFile := packageImports(t, ".")
	for file, imports := range byFile {
		// Test files (this one included) legitimately use "os" for
		// repository-shape checks like TestNoCmdEntrypoint above; only the
		// package's own non-test source must never read an environment
		// variable.
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		for _, imp := range imports {
			if imp == "os" {
				t.Fatalf("%s imports %q: this substep must not read any environment variable", file, imp)
			}
		}
	}
}
