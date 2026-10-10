package alertnotify

// This test verifies, by static import inspection rather than by trusting a
// docstring, that this package -- the one deliberate exception to the
// notify-delivery family's own "never import internal/alerts" rule -- never
// imports anything beyond what its own narrow bridge role requires: no
// incident/unit-status/CAD authority, no live HTTP route, no provider SDK,
// no eligibility-evaluation/orchestration/send authority over its
// notify-family siblings, and no direct read of the alert tables themselves
// (internal/alertstore/internal/alertpipeline are both forbidden: this
// package accepts an already-validated alerts.Event from its caller, it
// never reads alert_events or calls RecordDetection itself).

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenImportSubstrings never legitimately appears in an import path of
// this package, in any file (test files included). internal/alerts is
// deliberately absent from this list -- the one intended exception that
// makes this package what it is.
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
	// This bridge accepts an already-validated alerts.Event from its
	// caller; it never reads alert_events itself and never calls
	// RecordDetection -- both of these remain forbidden even though
	// internal/alerts itself is required.
	"internal/alertpipeline",
	"internal/alertstore",
	// This bridge creates outbox rows only: it must never gain
	// eligibility-evaluation, relay/send, consent, preference, or
	// orchestration authority over its notify-family siblings.
	"notifyconsent",
	"notifyconsentstore",
	"notifyprefs",
	"notifyprefsstore",
	"notifypreferences",
	"notifyattempt",
	"notifyrelay",
	"notifywebpush",
	"notifyworker",
	"notifyoutcome",
	"onesignal",
	"net/http",
	"net/smtp",
	"net/mail",
}

// productionOnlyForbiddenImportSubstrings additionally forbids the
// concrete store packages in this package's own NON-TEST source only:
// EnqueueMatchedEvent depends solely on the narrow local interfaces
// OutboxStore/DeviceLister, shaped after
// notifyoutboxstore.Enqueue/notifydevicestore.ListActiveTestMode, exactly
// mirroring notifyworker's own "never the concrete store type in
// production code" convention. Test files are exempt: unit tests use
// fakes, but the live integration test legitimately constructs real store
// instances to satisfy these same interfaces against a real database.
var productionOnlyForbiddenImportSubstrings = []string{
	"notifyoutboxstore",
	"notifydevicestore",
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
						"this bridge must never gain incident/unit-status/CAD authority, "+
						"a live HTTP route, a provider SDK, or eligibility-evaluation/"+
						"orchestration/send authority over its notify-family siblings",
						file, imp, forbidden)
				}
			}
		}
	}
}

// TestProductionCodeNeverImportsConcreteStores enforces the "narrow local
// interfaces only" design discipline described above as a tested
// invariant, not just a convention: alertnotify.go itself must never import
// notifyoutboxstore or notifydevicestore. Test files are exempt -- they
// legitimately construct real store instances to satisfy the same
// interfaces against a real database.
func TestProductionCodeNeverImportsConcreteStores(t *testing.T) {
	byFile := packageImports(t, ".")
	for file, imports := range byFile {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		for _, imp := range imports {
			for _, forbidden := range productionOnlyForbiddenImportSubstrings {
				if strings.Contains(imp, forbidden) {
					t.Fatalf("%s imports %q, which matches forbidden substring %q: "+
						"this package's own non-test source must depend only on its "+
						"narrow local interfaces (OutboxStore, DeviceLister), never the "+
						"concrete store packages directly",
						file, imp, forbidden)
				}
			}
		}
	}
}

// TestNoCmdEntrypoint documents that this part adds no new backend/cmd
// binary: the bridge is dormant unless a test, or a future authorized
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
		if strings.Contains(e.Name(), "alertnotify") {
			t.Fatalf("unexpected backend/cmd/%s: this part must not add a live entrypoint for this package", e.Name())
		}
	}
}

// TestNotRegisteredByAPIServer documents that backend/cmd/api/main.go never
// imports this package in Part 1.
func TestNotRegisteredByAPIServer(t *testing.T) {
	path := filepath.Join("..", "..", "cmd", "api", "main.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		if strings.Contains(p, "alertnotify") {
			t.Fatalf("cmd/api/main.go imports %q: this part must not register the bridge with the live server", p)
		}
	}
}
