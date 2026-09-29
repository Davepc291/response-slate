package notifyattempt

// This test verifies, by static import inspection rather than by trusting
// a docstring, that this package never imports anything that could give
// it provider/network/sending authority, a live HTTP route, a worker/
// poller mechanism, outbox-mutation or delivery-audit-writing authority,
// live radio/CAD wiring, or production configuration. Modeled on
// backend/internal/notifypreferences/depcheck_test.go: this package
// legitimately depends on notifyoutbox and notifypreferences (the plain
// domain/evaluation packages), so those are not forbidden -- only the
// store packages that carry real mutation/write authority are.

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenImportSubstrings never legitimately appears in an import path
// of this package. Substring matching (not exact package names) so a
// future forbidden-package rename still gets caught.
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
	// This substep is a pure planner: it must never gain outbox-mutation
	// or delivery-audit-writing authority, must never itself consume
	// alert_events beyond the caller-supplied AlertContext, and must
	// never claim/lease/poll anything.
	"internal/alerts",
	"internal/alertpipeline",
	"internal/alertstore",
	"notifyoutboxstore",
	"notifydeliverystore",
	"notifydevicestore", // this package only needs notifydevices.DeviceID, never the store
	"notifyconsent",
	"notifyconsentstore",
	"notifyprefsstore",
	"notifyrelay", // guarded in advance: no such package exists yet
	"onesignal",
	"net/http",
	"net/smtp",
	"net/mail",
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
						"this package must never gain provider/network/sending authority, "+
						"a live HTTP route, worker/poller authority, outbox-mutation or "+
						"delivery-audit-writing authority, or live radio/CAD wiring",
						file, imp, forbidden)
				}
			}
		}
	}
}

// TestNoCmdEntrypoint documents that this substep adds no new backend/cmd
// binary: the package is dormant unless a test, or a future authorized
// integration, constructs and calls it explicitly.
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
// environment variable: it is a pure planning package with no
// persistence or configuration of its own.
func TestNoEnvironmentVariables(t *testing.T) {
	byFile := packageImports(t, ".")
	for file, imports := range byFile {
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
