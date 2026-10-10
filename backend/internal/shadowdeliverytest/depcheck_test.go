package shadowdeliverytest

// This test verifies, by static import inspection rather than by trusting a
// docstring, that this test-only package adds no new backend/cmd binary and
// is never imported by backend/cmd/api/main.go. It carries no
// forbidden-import list of its own: this package is the one deliberate
// exception in the repository that is SUPPOSED to import packages on both
// sides of the alert-family/notify-family reverse-dependency guards (see
// this package's own doc comment in doc.go) -- nothing else may ever import
// IT back, since it declares no exported symbol and no other package has
// any reason to reference it.

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoCmdEntrypoint documents that this part adds no new backend/cmd
// binary: this package is dormant unless `go test` itself constructs and
// runs its live test explicitly. This is a repository-shape check, not a Go
// dependency check.
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
		if strings.Contains(e.Name(), "shadow") {
			t.Fatalf("unexpected backend/cmd/%s: this test-only package must not gain a live entrypoint", e.Name())
		}
	}
}

// TestNotRegisteredByAPIServer documents that backend/cmd/api/main.go never
// imports this package.
func TestNotRegisteredByAPIServer(t *testing.T) {
	path := filepath.Join("..", "..", "cmd", "api", "main.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		if strings.Contains(p, "shadowdeliverytest") {
			t.Fatalf("cmd/api/main.go imports %q: this test-only package must never be wired into the live server", p)
		}
	}
}
