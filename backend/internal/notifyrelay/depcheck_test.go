package notifyrelay

// This test verifies, by static import inspection rather than by trusting a
// docstring, that this package never imports anything that could translate
// the provider-neutral relay contract into a live network capability, a
// provider SDK, incident/unit-status/CAD authority, a live HTTP route, a
// live radio/alert trigger, worker/claim/outbox-mutation authority,
// delivery-audit-orchestration authority, or eligibility-evaluation
// authority. Modeled directly on backend/internal/notifydelivery/
// depcheck_test.go and backend/internal/notifyoutbox/depcheck_test.go,
// extended for this package's own scope (Step 8D-B Part 11,
// docs/notification-relay-amendment-v1.md Section 4.3).
//
// Unlike its notify-family siblings, this package's forbidden list also
// includes every other notify-family package and store: per notifyrelay.go's
// own doc comment, this package is the outermost, network-adjacent boundary
// of the whole relay design and must remain a fully independent leaf, so
// that no future concrete provider client can inherit eligibility-
// evaluation, outbox, or delivery-audit authority merely by sharing a type
// with this package.

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
	"internal/identity",
	"internal/identitystore",
	"internal/alerts",
	"internal/alertpipeline",
	"internal/alertstore",
	// This substep is a provider-neutral contract only: it must remain
	// fully independent of every other notify-family package and store
	// (see this file's own header comment), never gaining outbox-mutation,
	// delivery-audit-orchestration, or eligibility-evaluation authority by
	// depending on any of them.
	"notifydevices",
	"notifydevicestore",
	"notifyconsent",
	"notifyconsentstore",
	"notifyprefs",
	"notifyprefsstore",
	"notifyoutbox",
	"notifyoutboxstore",
	"notifydelivery",
	"notifydeliverystore",
	"notifypreferences",
	"notifyattempt",
	// No provider SDK, and no third-party Web Push client library, is
	// authorized by this part: this package implements the contract only,
	// never a concrete provider integration.
	"onesignal",
	"webpush-go",
	"sherclockholmes",
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
						"this package must remain a fully independent, network-free, "+
						"provider-neutral contract with no outbox/delivery-audit/"+
						"eligibility-evaluation authority and no provider SDK",
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
// environment variable: it is a pure contract package, and environment
// variable wiring (GFR_NOTIFY_*, per
// docs/notification-relay-amendment-v1.md Section 20) is explicitly out of
// scope until a later, separately authorized substep implements a concrete
// Sender.
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
