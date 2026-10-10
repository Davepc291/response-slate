package alertnotify

import (
	"context"
	"errors"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/alerts"
	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
)

// fakeOutboxStore is a minimal in-memory OutboxStore fake. It records every
// call it receives and, for a given device ID, returns the configured
// err (if any) instead of an Entry.
type fakeOutboxStore struct {
	calls   []fakeEnqueueCall
	failFor map[notifydevices.DeviceID]error
}

type fakeEnqueueCall struct {
	now            time.Time
	eventID        string
	deviceID       notifydevices.DeviceID
	userID         identity.UserID
	maxAttempts    int
	firstAttemptAt time.Time
	expiresAt      time.Time
}

func (f *fakeOutboxStore) Enqueue(ctx context.Context, now time.Time, eventID string, deviceID notifydevices.DeviceID,
	userID identity.UserID, maxAttempts int, firstAttemptAt, expiresAt time.Time) (notifyoutbox.Entry, error) {
	f.calls = append(f.calls, fakeEnqueueCall{now, eventID, deviceID, userID, maxAttempts, firstAttemptAt, expiresAt})
	if err, ok := f.failFor[deviceID]; ok {
		return notifyoutbox.Entry{}, err
	}
	return notifyoutbox.Entry{
		EventID:  eventID,
		DeviceID: deviceID,
		UserID:   userID,
		State:    notifyoutbox.StatePending,
	}, nil
}

// fakeDeviceLister is a minimal in-memory DeviceLister fake.
type fakeDeviceLister struct {
	devices []notifydevices.Registration
	err     error
	calls   int
}

func (f *fakeDeviceLister) ListActiveTestMode(ctx context.Context) ([]notifydevices.Registration, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.devices, nil
}

func matchedEvent() alerts.Event {
	return alerts.Event{
		EventID:        "evt-1",
		State:          alerts.StateMatched,
		ExpiresAt:      time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		DisplaySummary: "Structure fire reported",
	}
}

func TestNewServiceRejectsNilOutbox(t *testing.T) {
	_, err := NewService(nil, &fakeDeviceLister{}, Config{MaxAttempts: 3})
	if !errors.Is(err, ErrUnconfigured) {
		t.Fatalf("got err %v, want ErrUnconfigured", err)
	}
}

func TestNewServiceRejectsNilDeviceLister(t *testing.T) {
	_, err := NewService(&fakeOutboxStore{}, nil, Config{MaxAttempts: 3})
	if !errors.Is(err, ErrUnconfigured) {
		t.Fatalf("got err %v, want ErrUnconfigured", err)
	}
}

func TestNewServiceRejectsZeroMaxAttempts(t *testing.T) {
	_, err := NewService(&fakeOutboxStore{}, &fakeDeviceLister{}, Config{MaxAttempts: 0})
	if !errors.Is(err, ErrLimits) {
		t.Fatalf("got err %v, want ErrLimits", err)
	}
}

func TestNewServiceRejectsNegativeMaxAttempts(t *testing.T) {
	_, err := NewService(&fakeOutboxStore{}, &fakeDeviceLister{}, Config{MaxAttempts: -1})
	if !errors.Is(err, ErrLimits) {
		t.Fatalf("got err %v, want ErrLimits", err)
	}
}

func TestNewServiceAcceptsValidConfig(t *testing.T) {
	svc, err := NewService(&fakeOutboxStore{}, &fakeDeviceLister{}, Config{MaxAttempts: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc == nil {
		t.Fatal("expected non-nil Service")
	}
}

func TestEnqueueMatchedEventRejectsZeroNow(t *testing.T) {
	svc, _ := NewService(&fakeOutboxStore{}, &fakeDeviceLister{}, Config{MaxAttempts: 3})
	_, err := svc.EnqueueMatchedEvent(context.Background(), time.Time{}, matchedEvent())
	if !errors.Is(err, ErrInput) {
		t.Fatalf("got err %v, want ErrInput", err)
	}
}

// TestEnqueueMatchedEventRejectsNonMatchedState is the core safety proof:
// every non-matched state must perform zero device or outbox operations,
// never relying on EventCreated (which is true for ambiguous/partial/
// low_confidence too).
func TestEnqueueMatchedEventRejectsNonMatchedState(t *testing.T) {
	for _, st := range []alerts.State{alerts.StateAmbiguous, alerts.StatePartial, alerts.StateLowConfidence, alerts.State("rejected")} {
		t.Run(string(st), func(t *testing.T) {
			outbox := &fakeOutboxStore{}
			devices := &fakeDeviceLister{devices: []notifydevices.Registration{{ID: 1, UserID: 10, TestMode: true}}}
			svc, _ := NewService(outbox, devices, Config{MaxAttempts: 3})

			ev := matchedEvent()
			ev.State = st
			_, err := svc.EnqueueMatchedEvent(context.Background(), time.Now(), ev)

			if !errors.Is(err, ErrNotMatched) {
				t.Fatalf("got err %v, want ErrNotMatched", err)
			}
			if devices.calls != 0 {
				t.Fatalf("ListActiveTestMode called %d times, want 0", devices.calls)
			}
			if len(outbox.calls) != 0 {
				t.Fatalf("Enqueue called %d times, want 0", len(outbox.calls))
			}
		})
	}
}

func TestEnqueueMatchedEventZeroEligibleDevices(t *testing.T) {
	outbox := &fakeOutboxStore{}
	devices := &fakeDeviceLister{devices: nil}
	svc, _ := NewService(outbox, devices, Config{MaxAttempts: 3})

	results, err := svc.EnqueueMatchedEvent(context.Background(), time.Now(), matchedEvent())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("got %d results, want 0", len(results))
	}
	if len(outbox.calls) != 0 {
		t.Fatalf("Enqueue called %d times, want 0", len(outbox.calls))
	}
}

func TestEnqueueMatchedEventListActiveTestModeErrorPropagates(t *testing.T) {
	wantErr := errors.New("boom")
	outbox := &fakeOutboxStore{}
	devices := &fakeDeviceLister{err: wantErr}
	svc, _ := NewService(outbox, devices, Config{MaxAttempts: 3})

	results, err := svc.EnqueueMatchedEvent(context.Background(), time.Now(), matchedEvent())
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want %v", err, wantErr)
	}
	if results != nil {
		t.Fatalf("got results %v, want nil", results)
	}
	if len(outbox.calls) != 0 {
		t.Fatalf("Enqueue called %d times, want 0", len(outbox.calls))
	}
}

// TestEnqueueMatchedEventFanOutMultipleDevicesMultipleUsers proves this
// bridge performs exactly one Enqueue call per eligible device, spanning
// multiple distinct users, with the exact locked argument values:
// firstAttemptAt == now, expiresAt == ev.ExpiresAt, eventID == ev.EventID,
// maxAttempts == Config.MaxAttempts, deviceID/userID from each Registration.
func TestEnqueueMatchedEventFanOutMultipleDevicesMultipleUsers(t *testing.T) {
	outbox := &fakeOutboxStore{}
	devices := &fakeDeviceLister{devices: []notifydevices.Registration{
		{ID: 101, UserID: 1, TestMode: true},
		{ID: 102, UserID: 2, TestMode: true},
		{ID: 103, UserID: 2, TestMode: true},
	}}
	svc, _ := NewService(outbox, devices, Config{MaxAttempts: 7})

	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	ev := matchedEvent()
	results, err := svc.EnqueueMatchedEvent(context.Background(), now, ev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if len(outbox.calls) != 3 {
		t.Fatalf("Enqueue called %d times, want 3", len(outbox.calls))
	}

	wantDeviceUser := map[notifydevices.DeviceID]identity.UserID{101: 1, 102: 2, 103: 2}
	seen := map[notifydevices.DeviceID]bool{}
	for i, call := range outbox.calls {
		wantUser, ok := wantDeviceUser[call.deviceID]
		if !ok {
			t.Fatalf("call %d: unexpected deviceID %d", i, call.deviceID)
		}
		if call.userID != wantUser {
			t.Fatalf("call %d: userID = %d, want %d", i, call.userID, wantUser)
		}
		if call.eventID != ev.EventID {
			t.Fatalf("call %d: eventID = %q, want %q", i, call.eventID, ev.EventID)
		}
		if !call.now.Equal(now) {
			t.Fatalf("call %d: now = %v, want %v", i, call.now, now)
		}
		if !call.firstAttemptAt.Equal(now) {
			t.Fatalf("call %d: firstAttemptAt = %v, want %v (must equal now)", i, call.firstAttemptAt, now)
		}
		if !call.expiresAt.Equal(ev.ExpiresAt) {
			t.Fatalf("call %d: expiresAt = %v, want ev.ExpiresAt %v", i, call.expiresAt, ev.ExpiresAt)
		}
		if call.maxAttempts != 7 {
			t.Fatalf("call %d: maxAttempts = %d, want 7 (Config.MaxAttempts)", i, call.maxAttempts)
		}
		seen[call.deviceID] = true
	}
	for id := range wantDeviceUser {
		if !seen[id] {
			t.Fatalf("device %d was never passed to Enqueue", id)
		}
	}

	for i, r := range results {
		if r.Err != nil {
			t.Fatalf("result %d: unexpected Err %v", i, r.Err)
		}
	}
}

// TestEnqueueMatchedEventOneDeviceFailureContinuesFanOut proves a per-device
// Enqueue error is captured in that device's own DeviceResult.Err and never
// escalated to a function-level error, and that the fan-out continues to
// every remaining device regardless.
func TestEnqueueMatchedEventOneDeviceFailureContinuesFanOut(t *testing.T) {
	failErr := errors.New("device 2 enqueue failed")
	outbox := &fakeOutboxStore{failFor: map[notifydevices.DeviceID]error{202: failErr}}
	devices := &fakeDeviceLister{devices: []notifydevices.Registration{
		{ID: 201, UserID: 1, TestMode: true},
		{ID: 202, UserID: 1, TestMode: true},
		{ID: 203, UserID: 1, TestMode: true},
	}}
	svc, _ := NewService(outbox, devices, Config{MaxAttempts: 3})

	results, err := svc.EnqueueMatchedEvent(context.Background(), time.Now(), matchedEvent())
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3 (fan-out must continue past the failing device)", len(results))
	}
	if len(outbox.calls) != 3 {
		t.Fatalf("Enqueue called %d times, want 3", len(outbox.calls))
	}

	var failing *DeviceResult
	for i := range results {
		if results[i].DeviceID == 202 {
			failing = &results[i]
		}
	}
	if failing == nil {
		t.Fatal("expected a result for device 202")
	}
	if !errors.Is(failing.Err, failErr) {
		t.Fatalf("device 202 result.Err = %v, want %v", failing.Err, failErr)
	}

	for _, id := range []notifydevices.DeviceID{201, 203} {
		var r *DeviceResult
		for i := range results {
			if results[i].DeviceID == id {
				r = &results[i]
			}
		}
		if r == nil {
			t.Fatalf("expected a result for device %d", id)
		}
		if r.Err != nil {
			t.Fatalf("device %d result.Err = %v, want nil", id, r.Err)
		}
	}
}

// TestEnqueueMatchedEventRepeatedCallSameEventRelyOnEnqueueIdempotency
// proves this package performs no deduplication of its own: it simply
// calls Enqueue again for the same (eventID, deviceID) pair on a repeated
// call, trusting Enqueue's own (event_id, device_id) uniqueness constraint
// to make that call a no-op at the database layer. The fake here does not
// model that constraint (it is a database-level guarantee, exercised by the
// live integration test instead); this test only proves alertnotify issues
// the call again rather than skipping it.
func TestEnqueueMatchedEventRepeatedCallCallsEnqueueAgain(t *testing.T) {
	outbox := &fakeOutboxStore{}
	devices := &fakeDeviceLister{devices: []notifydevices.Registration{{ID: 301, UserID: 1, TestMode: true}}}
	svc, _ := NewService(outbox, devices, Config{MaxAttempts: 3})

	ev := matchedEvent()
	if _, err := svc.EnqueueMatchedEvent(context.Background(), time.Now(), ev); err != nil {
		t.Fatalf("first call: unexpected error: %v", err)
	}
	if _, err := svc.EnqueueMatchedEvent(context.Background(), time.Now(), ev); err != nil {
		t.Fatalf("second call: unexpected error: %v", err)
	}
	if len(outbox.calls) != 2 {
		t.Fatalf("Enqueue called %d times across two EnqueueMatchedEvent calls, want 2", len(outbox.calls))
	}
}
