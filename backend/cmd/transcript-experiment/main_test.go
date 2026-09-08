package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestExplicitArgumentsOnly(t *testing.T) {
	t.Setenv("GFR_DATABASE_URL", "SYNTHETIC SECRET")
	for _, args := range [][]string{nil, {"--dataset", "relative"}, {"--base-url", "http://example.invalid"}, {"--bearer-token", "SYNTHETIC SECRET"}, {"--name", "test"}} {
		var out, errs bytes.Buffer
		if run(context.Background(), args, &out, &errs) != 2 || out.Len() != 0 || strings.Contains(errs.String(), "SECRET") {
			t.Fatal("unsafe arguments")
		}
	}
}
