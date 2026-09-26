package notifydevicestore

// This test verifies, by static import inspection rather than by trusting a
// docstring, that this package never imports anything that could translate
// device-registration persistence into incident/unit-status/CAD authority, a
// live HTTP route, a provider SDK, an outbound mail/network capability, or a
// relay/outbox capability that would let it actually send anything.
// Modeled directly on backend/internal/notifydevices/depcheck_test.go
// (itself modeled on backend/internal/alertpipeline/depcheck_test.go),
// extended for this package's own scope: unlike notifydevices, this package
// is specifically the PostgreSQL persistence adapter, so pgx/PostgreSQL
// imports are the one category deliberately NOT forbidden here.

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
// forbidden-package rename still gets caught, and so a not-yet-existing
// future package (notifyoutbox, notifyrelay) is guarded against in advance,
// not only after it first appears.
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
	// This substep is device registration persistence only: it must not
	// itself consume or trigger from alert events (that is the future
	// notifyoutbox package's job, gated on its own separate authorization),
	// and it must never gain a relay capability of its own.
	"internal/alerts",
	"internal/alertpipeline",
	"internal/alertstore",
	"notifyoutbox", // guarded in advance: no such package exists yet
	"notifyrelay",  // guarded in advance: no such package exists yet
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
						"a live HTTP route, a provider SDK, or a relay/outbox capability of its own",
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
