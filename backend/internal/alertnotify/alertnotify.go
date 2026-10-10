// Package alertnotify is the Step 8D-C Part 1 alert -> notification outbox
// bridge: the single, narrow composition layer authorized to turn a
// validated, already-persisted matched alerts.Event into
// notifyoutboxstore.Enqueue calls, one per eligible device. It is the one
// deliberate exception to the notify-delivery family's own long-standing
// rule ("never import internal/alerts") -- this package exists specifically
// so that rule can keep holding everywhere else.
//
// This package depends on only the pure alerts domain type plus the
// minimum notification/device/outbox types and interfaces necessary: it
// declares narrow local interfaces (OutboxStore, DeviceLister) shaped after
// notifyoutboxstore.Enqueue and notifydevicestore.ListActiveTestMode,
// exactly mirroring backend/internal/notifyworker's own "never import the
// concrete store package, only the domain types it returns" convention. Its
// own depcheck_test.go enforces this as a tested invariant, not just a
// convention: notifyoutboxstore and notifydevicestore are forbidden in this
// package's own non-test source (only its tests construct real instances
// to satisfy these same interfaces against a real database).
//
// This package never imports internal/alertstore or internal/alertpipeline
// either: it never reads alert_events itself and never calls
// RecordDetection. The caller -- whoever invokes alertpipeline.Service's own
// Process* methods -- already holds the alerts.Event locally after
// RecordDetection returns, and passes it in here directly.
//
// EnqueueMatchedEvent independently requires ev.State == alerts.StateMatched
// before performing any device or outbox operation at all, never trusting
// the caller to have already filtered this (EventCreated alone is not
// sufficient: it can be true for ambiguous/partial/low_confidence events
// too). The database's own validate_notification_outbox_event() trigger
// (migration 000010, unchanged by this package) remains the independent,
// unbypassable final backstop regardless of any bug here.
//
// This package creates outbox rows only -- it never constructs a
// notifyrelay.Payload or a notifyworker.AlertData, never calls
// notifyworker, and is never registered by backend/cmd/api. Alert
// persistence (RecordDetection) and this package's own fan-out are
// deliberately NOT one transaction: a notification-side failure must never
// roll back or invalidate an already-persisted alert. Enqueue's own
// (event_id, device_id) idempotency (migration 000010, unchanged) is the
// only deduplication mechanism this package relies on or needs; it
// implements no deduplication of its own. A crash between a matched
// alert's own persistence and this package's fan-out ever running at all is
// a known, deliberately deferred gap: nothing here automatically re-drives
// a missed enqueue step, though re-running EnqueueMatchedEvent for the same
// event later is always safe.
package alertnotify

import (
	"context"
	"errors"
	"time"

	"greenwich-fire-responder/backend/internal/alerts"
	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/notifydevices"
	"greenwich-fire-responder/backend/internal/notifyoutbox"
)

// Sentinel errors. None of these, nor any error this package returns
// directly, ever echoes a caller-supplied value.
var (
	// ErrUnconfigured marks a nil OutboxStore or DeviceLister passed to
	// NewService.
	ErrUnconfigured = errors.New("alertnotify: one or more required dependencies are nil")
	// ErrLimits marks a non-positive Config.MaxAttempts. There is no
	// implicit default: a caller must choose a positive value explicitly,
	// mirroring alerts.Limits/alertpipeline.Limits' own identical
	// "construction fails closed without an explicit value" convention.
	ErrLimits = errors.New("alertnotify: MaxAttempts must be explicitly configured and positive")
	// ErrInput marks a zero now passed to EnqueueMatchedEvent.
	ErrInput = errors.New("alertnotify: invalid input")
	// ErrNotMatched marks an alerts.Event whose State is not
	// alerts.StateMatched. This package independently enforces this gate:
	// alertstore.Result.EventCreated alone is never sufficient
	// authorization, since it can be true for ambiguous/partial/
	// low_confidence events too, which must never produce an outbox entry
	// (migration 000010's own Section 11 evidence-state gating).
	ErrNotMatched = errors.New("alertnotify: event is not in the matched state")
)

// OutboxStore is the minimal notifyoutboxstore surface this package needs.
// Declared locally (rather than depending on the concrete
// *notifyoutboxstore.Postgres type) so this package is testable with a
// simple fake, and so its own depcheck_test.go can forbid importing
// notifyoutboxstore in this file at all -- exactly mirroring
// notifyworker.OutboxStore's identical convention.
type OutboxStore interface {
	Enqueue(ctx context.Context, now time.Time, eventID string, deviceID notifydevices.DeviceID,
		userID identity.UserID, maxAttempts int, firstAttemptAt, expiresAt time.Time) (notifyoutbox.Entry, error)
}

// DeviceLister is the minimal notifydevicestore surface this package needs:
// a system-wide, test_mode=true-only enumeration (Step 8D-C Part 1's own
// DEV isolation gate -- see notifydevicestore.ListActiveTestMode's own doc
// comment).
type DeviceLister interface {
	ListActiveTestMode(ctx context.Context) ([]notifydevices.Registration, error)
}

// Config is explicit, required configuration. There is no default.
type Config struct {
	// MaxAttempts must be positive. NewService fails closed otherwise.
	// Section 14 of docs/notification-relay-amendment-v1.md leaves the
	// actual retry-count policy unresolved; this package assumes no
	// default of its own and invents no production retry policy.
	MaxAttempts int
}

// Service is the Step 8D-C Part 1 bridge. Construct with NewService.
type Service struct {
	outbox  OutboxStore
	devices DeviceLister
	cfg     Config
}

// NewService validates deps and cfg and returns a ready-to-use Service, or
// refuses construction entirely (nil, error) -- fail closed, mirroring
// notifyworker.New's own identical locked design.
func NewService(outbox OutboxStore, devices DeviceLister, cfg Config) (*Service, error) {
	if outbox == nil || devices == nil {
		return nil, ErrUnconfigured
	}
	if cfg.MaxAttempts <= 0 {
		return nil, ErrLimits
	}
	return &Service{outbox: outbox, devices: devices, cfg: cfg}, nil
}

// DeviceResult reports one device's own outcome of one EnqueueMatchedEvent
// call. Err is nil on success. A non-nil Err here is never escalated to a
// function-level error: one device's own failure must never prevent
// attempts for the remaining eligible devices.
type DeviceResult struct {
	DeviceID notifydevices.DeviceID
	Entry    notifyoutbox.Entry
	Err      error
}

// EnqueueMatchedEvent fans a validated, already-persisted matched alert
// event out to every currently active, test_mode=true device, calling
// Enqueue exactly once per device.
//
// now must be non-zero (ErrInput otherwise); this package never calls
// time.Now() itself. ev.State must already equal alerts.StateMatched
// (ErrNotMatched otherwise) -- checked before ListActiveTestMode or Enqueue
// are ever called, so a non-matched event performs zero device or outbox
// operations of any kind.
//
// If ListActiveTestMode itself fails, this returns (nil, err) immediately,
// propagating that error unchanged: this is a precondition failure before
// any device was even enumerated, not a per-device outcome. Otherwise, this
// always returns (results, nil): a per-device Enqueue failure is captured
// in that device's own DeviceResult.Err and the loop continues to every
// remaining device regardless.
//
// firstAttemptAt is always now; expiresAt is always ev.ExpiresAt (Section
// 10's own already-documented inheritance); maxAttempts is always the
// Service's own explicitly configured Config.MaxAttempts. Enqueue's own
// (event_id, device_id) idempotency means calling this repeatedly for the
// same event is always safe and never produces a duplicate row.
func (s *Service) EnqueueMatchedEvent(ctx context.Context, now time.Time, ev alerts.Event) ([]DeviceResult, error) {
	if now.IsZero() {
		return nil, ErrInput
	}
	if ev.State != alerts.StateMatched {
		return nil, ErrNotMatched
	}

	devices, err := s.devices.ListActiveTestMode(ctx)
	if err != nil {
		return nil, err
	}

	results := make([]DeviceResult, 0, len(devices))
	for _, d := range devices {
		entry, err := s.outbox.Enqueue(ctx, now, ev.EventID, d.ID, d.UserID, s.cfg.MaxAttempts, now, ev.ExpiresAt)
		results = append(results, DeviceResult{DeviceID: d.ID, Entry: entry, Err: err})
	}
	return results, nil
}
