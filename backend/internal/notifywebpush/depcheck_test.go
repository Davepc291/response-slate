package notifywebpush

// This test verifies, by static import inspection rather than by trusting a
// docstring, that this package never imports anything that could translate
// the concrete Web Push sender into incident/unit-status/CAD authority, a
// live HTTP route, a live radio/alert trigger, worker/outbox-mutation
// authority, delivery-audit-orchestration authority, device-revocation
// authority, eligibility-evaluation authority, a second push/provider SDK,
// or direct environment-variable/config wiring. Modeled directly on
// backend/internal/notifyrelay/depcheck_test.go, extended for this
// package's own scope (Step 8D-B Part 12,
// docs/notification-relay-amendment-v1.md Section 4.3).
//
// Unlike notifyrelay's own depcheck, this package is explicitly allowed
// net/http (it is the one place in the whole notify family authorized to do
// real HTTP/network I/O) and notifyrelay itself (whose Sender interface it
// implements). It must depend on nothing else beyond the standard library,
// notifyrelay, and github.com/SherClockHolmes/webpush-go (declared in
// go.mod; this file does not re-verify go.mod contents, only this
// package's own .go source).

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
	"internal/config",     // no GFR_* env var / config.Load wiring yet
	"internal/identity",
	"internal/identitystore",
	"internal/alerts",
	"internal/alertpipeline",
	"internal/alertstore",
	// This substep is a concrete Sender only: it must never gain outbox-
	// mutation, delivery-audit-orchestration, device-revocation, or
	// eligibility-evaluation authority by depending on any notify-family
	// sibling other than notifyrelay (whose interface it implements).
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
	// No second push/provider SDK is authorized: this package's only
	// authorized concrete dependency is webpush-go (direct Web Push).
	"onesignal",
	"net/smtp",
	"net/mail",
	// This substep has no persistence of its own: a database import here
	// would mean persistence quietly arrived without the schema/migration
	// review that requires.
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
						"this package must remain a concrete direct-Web-Push Sender only, "+
						"with no outbox/delivery-audit/device-revocation/eligibility-evaluation "+
						"authority and no second provider SDK",
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
// environment variable: Options is always caller-supplied, already-
// validated configuration (see notifywebpush.go's own doc comment).
// config.Load wiring (turning GFR_NOTIFY_* variables into an Options
// value) is explicitly out of scope until a later, separately authorized
// substep.
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
