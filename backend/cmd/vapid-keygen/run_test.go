package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func fixedGenerator(privateKey, publicKey string, err error) Generator {
	return func() (string, string, error) { return privateKey, publicKey, err }
}

func TestRunEmitsOneValidPairLabeledDevSandbox(t *testing.T) {
	var out bytes.Buffer
	deps := Deps{Stdout: &out, Generate: fixedGenerator("test-private-key-value", "test-public-key-value", nil)}

	if err := Run(deps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := out.String()
	for _, want := range []string{
		"DEV/SANDBOX ONLY",
		"GFR_NOTIFY_VAPID_PUBLIC_KEY=test-public-key-value",
		"GFR_NOTIFY_VAPID_PRIVATE_KEY=test-private-key-value",
		"Never commit",
		"production",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
}

func TestRunEmitsExactlyOnePublicAndOnePrivateKeyLine(t *testing.T) {
	var out bytes.Buffer
	deps := Deps{Stdout: &out, Generate: fixedGenerator("priv", "pub", nil)}

	if err := Run(deps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := out.String()
	if strings.Count(text, "GFR_NOTIFY_VAPID_PUBLIC_KEY=") != 1 {
		t.Fatalf("expected exactly one public key line:\n%s", text)
	}
	if strings.Count(text, "GFR_NOTIFY_VAPID_PRIVATE_KEY=") != 1 {
		t.Fatalf("expected exactly one private key line:\n%s", text)
	}
}

func TestRunPropagatesGeneratorError(t *testing.T) {
	var out bytes.Buffer
	wantErr := errors.New("boom")
	deps := Deps{Stdout: &out, Generate: fixedGenerator("", "", wantErr)}

	err := Run(deps)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the error to wrap %v, got %v", wantErr, err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no output on generator failure, got %q", out.String())
	}
}

// TestRunNeverCallsGenerateMoreThanOnce proves exactly one pair is generated
// per invocation, never more.
func TestRunNeverCallsGenerateMoreThanOnce(t *testing.T) {
	calls := 0
	deps := Deps{Stdout: &bytes.Buffer{}, Generate: func() (string, string, error) {
		calls++
		return "priv", "pub", nil
	}}

	if err := Run(deps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected Generate to be called exactly once, got %d", calls)
	}
}
