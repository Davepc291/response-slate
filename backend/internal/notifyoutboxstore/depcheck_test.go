package notifyoutboxstore

// This test verifies, by static import inspection rather than by trusting a
// docstring, that this package never imports anything that could translate
// outbox persistence into incident/unit-status/CAD authority, a live HTTP
// route, a provider SDK, an outbound mail/network capability, eligibility-
// evaluation authority over its notify-family siblings, or orchestration
// authority above its own table. Modeled directly on
// backend/internal/notifydevicestore/depcheck_test.go (the closest existing
// precedent: a Postgres persistence adapter for this same family), added in
// Step 8D-B Part 14C because this package -- unlike every other notify-family
// package -- had no independent dependency-guard coverage before now, even
// though its own package doc comment already asserted several of these
// exact invariants in prose.
//
// Unlike notifydevices/notifyoutbox's own pure-domain guards, this package
// legitimately is a PostgreSQL persistence adapter, so pgx/PostgreSQL
// imports (and the stdlib database driver surface in general) are
// deliberately NOT forbidden here -- forbidding them would be exactly the
// kind of unnecessarily broad restriction that would block this package's
// own legitimate reason to exist.

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
// forbidden-package rename still gets caught. notifydevices, notifyoutbox,
// and identity (this package's own real, legitimate domain dependencies)
// are deliberately absent from this list.
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
	"internal/httpapi",    // no HTTP route in this package
	"internal/recordings", // no live radio/alert trigger wiring
	"internal/operations", // no shared live-server monitor coupling
	// This package is outbox bookkeeping persistence only: it must never
	// consume or trigger from alert events beyond carrying an opaque
	// event_id string, and it must never gain eligibility-evaluation,
	// relay/send, or orchestration authority over its notify-family
	// siblings -- all of that belongs to notifypreferences, notifyattempt,
	// notifyrelay/notifywebpush, and notifyworker/notifyoutcome
	// respectively, none of which this package needs or imports.
	"internal/alerts",
	"internal/alertpipeline",
	"internal/alertstore",
	"notifydevicestore",
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
	// Peer persistence store: composition across the two tables happens
	// only via notifyoutcome, which imports both -- neither store imports
	// the other directly, in either direction.
	"notifydeliverystore",
	// Step 8D-C Part 1: this package must never acquire a reverse
	// dependency on the alert->outbox bridge, which depends on IT, never
	// the other way.
	"alertnotify",
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
						"this package must never gain incident/unit-status/CAD authority, "+
						"a live HTTP route, a provider SDK, eligibility-evaluation authority, "+
						"or orchestration authority over its notify-family siblings",
						file, imp, forbidden)
				}
			}
		}
	}
}

// TestNoCmdEntrypoint documents that this package adds no new backend/cmd
// binary: it is dormant unless a test, or a future authorized integration,
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
		if strings.Contains(e.Name(), "notify") {
			t.Fatalf("unexpected backend/cmd/%s: this package must not add a live entrypoint", e.Name())
		}
	}
}

// TestNoEnvironmentVariables documents that this package reads no GFR_*
// environment variable, matching its own package doc comment's existing
// claim ("reads no GFR_NOTIFY_* environment variable") -- now test-enforced
// rather than prose-only.
func TestNoEnvironmentVariables(t *testing.T) {
	byFile := packageImports(t, ".")
	for file, imports := range byFile {
		// Test files (live-DB opt-in checks) legitimately use "os"; only
		// the package's own non-test source must never read an environment
		// variable.
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		for _, imp := range imports {
			if imp == "os" {
				t.Fatalf("%s imports %q: this package must not read any environment variable", file, imp)
			}
		}
	}
}
