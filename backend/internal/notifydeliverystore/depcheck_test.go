package notifydeliverystore

// This test verifies, by static import inspection rather than by trusting a
// docstring, that this package never imports anything that could translate
// delivery-audit persistence into incident/unit-status/CAD authority, a live
// HTTP route, a provider SDK, an outbound mail/network capability,
// eligibility-evaluation authority over its notify-family siblings, or
// orchestration authority above its own table. Modeled directly on
// backend/internal/notifydevicestore/depcheck_test.go (the closest existing
// precedent: a Postgres persistence adapter for this same family) and on
// backend/internal/notifyoutboxstore/depcheck_test.go (its own peer,
// added in the same Step 8D-B Part 14C pass, for the same reason: this
// package had no independent dependency-guard coverage before now.
//
// Unlike notifydevices/notifyoutbox's own pure-domain guards, this package
// legitimately is a PostgreSQL persistence adapter, so pgx/PostgreSQL
// imports are deliberately NOT forbidden here.

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
// forbidden-package rename still gets caught. notifydelivery, notifydevices,
// and notifyoutbox (this package's own real, legitimate domain dependencies)
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
	"internal/httpapi",
	"internal/recordings",
	"internal/operations",
	// This package is delivery-audit persistence only: it must never
	// consume or trigger from alert events, and it must never gain
	// eligibility-evaluation, relay/send, or orchestration authority over
	// its notify-family siblings -- all of that belongs to
	// notifypreferences, notifyattempt, notifyrelay/notifywebpush, and
	// notifyworker/notifyoutcome respectively, none of which this package
	// needs or imports.
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
	// Note: notifyoutboxstore is deliberately NOT forbidden here, unlike
	// the reverse direction (notifyoutboxstore's own guard does forbid
	// notifydeliverystore): this package's own production source
	// (notifydeliverystore.go) never imports notifyoutboxstore and has no
	// reason to -- composition across the two tables happens only via
	// notifyoutcome, which imports both. But this package's existing,
	// already-approved integration test legitimately constructs real
	// notification_outbox fixture rows via notifyoutboxstore.Enqueue, since
	// notification_deliveries' own composite foreign key requires a real,
	// internally consistent outbox row to reference -- forbidding it here
	// would be exactly the unnecessarily broad restriction the Part 14C
	// design lock warned against.
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
// claim -- now test-enforced rather than prose-only.
func TestNoEnvironmentVariables(t *testing.T) {
	byFile := packageImports(t, ".")
	for file, imports := range byFile {
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
