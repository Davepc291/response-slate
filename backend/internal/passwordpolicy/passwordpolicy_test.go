package passwordpolicy

import (
	"context"
	"strings"
	"testing"
)

// Every password value in this file is a synthetic test fixture, never a
// real credential.

func TestPolicyValidate(t *testing.T) {
	p := DefaultPolicy()
	if err := p.Validate("short-pw1"); err == nil {
		t.Error("expected too-short synthetic password to be rejected")
	}
	if err := p.Validate(strings.Repeat("a", 300)); err == nil {
		t.Error("expected oversized synthetic password to be rejected")
	}
	if err := p.Validate("synthetic-test-passphrase-1"); err != nil {
		t.Errorf("expected valid-length synthetic passphrase to pass: %v", err)
	}
	// No composition rule: an all-lowercase, no-digit passphrase must pass
	// length-only policy (Section 4: "no forced...composition rule").
	if err := p.Validate("correcthorsebatterystaple"); err != nil {
		t.Errorf("expected composition-free synthetic passphrase to pass: %v", err)
	}
	if err := p.Validate(string([]byte{0xff, 0xfe})); err != ErrPasswordInvalidEncoding {
		t.Errorf("expected invalid UTF-8 to be rejected, got %v", err)
	}
}

func TestHashAndVerifyRoundTrip(t *testing.T) {
	params := DefaultParams()
	params.Memory = 8 * 1024 // small for test speed only
	params.Iterations = 1

	encoded, err := Hash("synthetic-test-password-1", params)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := Verify(encoded, "synthetic-test-password-1")
	if err != nil || !ok {
		t.Fatalf("expected correct synthetic password to verify: ok=%v err=%v", ok, err)
	}
	ok, err = Verify(encoded, "synthetic-test-password-WRONG")
	if err != nil || ok {
		t.Fatalf("expected wrong synthetic password to fail verification: ok=%v err=%v", ok, err)
	}
}

func TestHashUsesUniqueSalt(t *testing.T) {
	params := DefaultParams()
	params.Memory = 8 * 1024
	params.Iterations = 1

	a, err := Hash("synthetic-test-password-1", params)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Hash("synthetic-test-password-1", params)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("expected two hashes of the identical synthetic password to differ (unique salt)")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	for _, bad := range []string{
		"",
		"plaintext-not-a-hash",
		"$argon2id$v=19$m=8,t=1,p=1$onlyonefield$",
		"$bcrypt$v=19$m=8,t=1,p=1$c2FsdA$aGFzaA",
	} {
		if _, err := Verify(bad, "anything"); err != ErrMalformedHash {
			t.Errorf("Verify(%q) = err %v, want ErrMalformedHash", bad, err)
		}
	}
}

func TestNeedsRehash(t *testing.T) {
	weak := Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}
	encoded, err := Hash("synthetic-test-password-1", weak)
	if err != nil {
		t.Fatal(err)
	}
	strong := DefaultParams()
	if !NeedsRehash(encoded, strong) {
		t.Error("expected a hash produced with weaker parameters to need rehashing")
	}
	if NeedsRehash(encoded, weak) {
		t.Error("expected a hash produced with identical parameters to not need rehashing")
	}
	if !NeedsRehash("not-a-valid-hash", strong) {
		t.Error("expected a malformed hash to always need rehashing")
	}
}

func TestNoOpBreachChecker(t *testing.T) {
	var c BreachChecker = NoOpBreachChecker{}
	breached, err := c.IsBreached(context.Background(), "synthetic-test-password-1")
	if err != nil || breached {
		t.Errorf("expected NoOpBreachChecker to always report not-breached, got breached=%v err=%v", breached, err)
	}
}
