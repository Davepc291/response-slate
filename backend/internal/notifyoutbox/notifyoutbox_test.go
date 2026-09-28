package notifyoutbox

import "testing"

func TestStateValid(t *testing.T) {
	valid := []State{StatePending, StateSent, StateExpired, StateCanceled, StateDeadLetter}
	for _, s := range valid {
		if !s.Valid() {
			t.Fatalf("expected %q to be valid", s)
		}
	}
	invalid := []State{"", "processing", "PENDING", "sent "}
	for _, s := range invalid {
		if s.Valid() {
			t.Fatalf("expected %q to be invalid", s)
		}
	}
}

func TestStateTerminal(t *testing.T) {
	if StatePending.Terminal() {
		t.Fatal("expected StatePending to not be terminal")
	}
	terminal := []State{StateSent, StateExpired, StateCanceled, StateDeadLetter}
	for _, s := range terminal {
		if !s.Terminal() {
			t.Fatalf("expected %q to be terminal", s)
		}
	}
	if (State("bogus")).Terminal() {
		t.Fatal("expected an invalid state to never be reported as terminal")
	}
}
