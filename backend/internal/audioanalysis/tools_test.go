package audioanalysis

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The Go test executable itself acts as the child: no FFmpeg installation needed.
func init() {
	index := -1
	for i, arg := range os.Args {
		if arg == "--audio-helper" {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	switch os.Args[index+1] {
	case "sleep":
		time.Sleep(10 * time.Second)
	case "stderr":
		for i := 0; i < 1024; i++ {
			io.WriteString(os.Stderr, strings.Repeat("x", 1024))
		}
		os.Exit(1)
	case "environment":
		if os.Getenv("GFR_DATABASE_URL") != "" || os.Getenv("FFREPORT") != "" {
			os.Exit(2)
		}
		io.WriteString(os.Stdout, "ok")
	case "arguments":
		io.WriteString(os.Stdout, os.Args[index+2])
	}
	os.Exit(0)
}

func TestProcessTools(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := func(mode string) []string { return []string{"-test.run=^TestToolHelper$", "--audio-helper", mode} }
	tools := ProcessTools{}
	t.Run("missing executable", func(t *testing.T) {
		if err := tools.Run(context.Background(), filepath.Join(t.TempDir(), "missing.exe"), nil, nil, io.Discard); err != errUnavailable {
			t.Fatal("unsafe missing-executable error")
		}
	})
	t.Run("timeout terminates child", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		start := time.Now()
		err := tools.Run(ctx, exe, args("sleep"), nil, io.Discard)
		if err != errTimeout || time.Since(start) > 3*time.Second {
			t.Fatalf("timeout did not reap child: %v", err)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := tools.Run(ctx, exe, args("sleep"), nil, io.Discard); err != errCanceled {
			t.Fatal("cancellation not propagated")
		}
	})
	t.Run("bounded stderr", func(t *testing.T) {
		b := &boundedBuffer{limit: 16384}
		n, err := b.Write(bytes.Repeat([]byte{'x'}, 1000000))
		if n != 1000000 || err != nil || b.Len() != 16384 || !b.truncated {
			t.Fatal("stderr not bounded")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := tools.Run(ctx, exe, args("stderr"), nil, io.Discard); err != errInvalid {
			t.Fatal("noisy child mishandled")
		}
	})
	t.Run("no secret environment", func(t *testing.T) {
		t.Setenv("GFR_DATABASE_URL", "secret")
		t.Setenv("FFREPORT", "secret")
		var out bytes.Buffer
		if err := tools.Run(context.Background(), exe, args("environment"), nil, &out); err != nil || out.String() != "ok" {
			t.Fatal("environment leaked")
		}
	})
	t.Run("literal arguments", func(t *testing.T) {
		var out bytes.Buffer
		literal := `spaces & $(not-a-command) ; "quotes"`
		if err := tools.Run(context.Background(), exe, append(args("arguments"), literal), nil, &out); err != nil || out.String() != literal {
			t.Fatal("arguments were interpreted")
		}
	})
}
