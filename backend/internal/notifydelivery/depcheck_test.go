package notifydelivery

// This test verifies, by static import inspection rather than by trusting a
// docstring, that this package never imports anything that could translate
// delivery-audit bookkeeping into incident/unit-status/CAD authority, a
// live HTTP route, a live radio/alert trigger, a provider SDK, worker/claim
// logic, eligibility-evaluation logic, or any network capability. Modeled
// directly on backend/internal/notifyoutbox/depcheck_test.go, extended for
// this package's own scope (Step 8D-B Part 7,
// docs/notification-relay-amendment-v1.md Section 17).

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
// forbidden-package rename still gets caught. Note this list deliberately
// does NOT include "notifyoutbox" or "notifydevices" themselves: this
// package reuses their own OutboxID/DeviceID types directly (see
// notifydelivery.go's own doc comment), exactly as notifyoutbox already
// reuses notifydevices.DeviceID/identity.UserID. Only the *store* packages
// (which carry persistence, and in notifyoutboxstore's case, state-machine
// authority) are forbidden.
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
	// This substep is delivery-audit bookkeeping only: it must not itself
	// consume or decide from alert events beyond carrying an opaque
	// event_id string, and it must never gain eligibility-evaluation or
	// outbox-orchestration authority by depending on the sibling stores --
	// that is the future notifypreferences (evaluation) and
	// attempt-orchestration components' job.
	"internal/alerts",
	"internal/alertpipeline",
	"internal/alertstore",
	"notifydevicestore",
	"notifyconsent",
	"notifyconsentstore",
	"notifyprefs",
	"notifyprefsstore",
	"notifyoutboxstore",
	"notifypreferences", // reserved for the future evaluation engine
	"notifyrelay",       // guarded in advance: no such package exists yet
	"onesignal",
	"net/http",
	"net/smtp",
	"net/mail",
	// This substep is a pure domain model with no persistence of its own: a
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
						"a live HTTP route, a live radio/alert trigger, a provider/network "+
						"capability, or eligibility-evaluation/outbox-orchestration authority",
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
// environment variable: it is a pure domain package, and environment
// variable wiring (GFR_NOTIFY_*, per
// docs/notification-relay-amendment-v1.md Section 25.5) is explicitly out
// of scope until a later, separately authorized substep.
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
