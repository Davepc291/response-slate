// Package notifyrelay is the Step 8D-B Part 11 provider-neutral relay
// contract (docs/notification-relay-amendment-v1.md Section 4.3's own
// reserved name: "notifyrelay | Server-only provider REST client... holds
// provider credentials exclusively"). This part implements only the
// contract that future component will satisfy: a Sender interface, its
// Request/Result shapes, and an inert fake implementation for deterministic
// tests. It does not implement that future component itself.
//
// Step 8D-B Part 11A's discovery selected direct standards-based Web Push
// (RFC 8291/8292) as the future concrete architecture, on the grounds that
// the existing notification_devices schema (endpoint/p256dh/auth) already
// matches it exactly, with zero schema change -- unlike a provider-managed
// alternative (OneSignal or similar), which would need its own additional
// registration flow and its own separate authorization. Nothing in this
// package depends on that choice: Sender, Request, and Result are equally
// satisfiable by a direct Web Push client, a provider-REST client, or (as
// here) a fully inert fake. This package itself never becomes, and never
// wraps, that concrete client.
//
// This package has no persistence, no HTTP handler, no route, no database
// migration, no environment variable, no provider SDK, and no network
// dependency of any kind. It performs no HTTP/network I/O, no VAPID
// signing, no Web Push payload encryption, and imports no third-party Web
// Push or provider library. It never claims, leases, or mutates a
// notification_outbox row, never writes a notification_deliveries row, and
// is never registered by backend/cmd/api: a caller (a test, or a future
// authorized worker/orchestration component) must construct and invoke it
// explicitly, mirroring the same dormant-by-construction pattern already
// established by every other notify-family package in this step.
//
// This package deliberately imports no other notify-family package and no
// sibling store: it is the outermost, network-adjacent boundary of the
// whole relay design, and must remain fully independent so that no future
// concrete provider client can inherit eligibility-evaluation, outbox, or
// delivery-audit authority merely by sharing a type with this package.
// OutboxID is therefore a plain local identity carrier, not
// notifyoutbox.OutboxID: a caller converts explicitly (see OutboxID's own
// doc comment).
//
// Step 8D-B Part 12B's design lock added two small, additive fields ahead
// of Part 12's concrete backend/internal/notifywebpush sender:
// Request.TestMode (a plain, opaque bool a concrete Sender needs to
// enforce its own environment/test-mode isolation policy without
// importing notifydevices or any store) and Result.RetryAfter (an
// optional, parsed backoff hint so that information is not silently
// discarded before a future orchestration caller exists to use it).
// Neither changes this package's own scope or authority: it still
// performs no HTTP/network I/O, no VAPID signing, no encryption, and no
// outbox/delivery-audit mutation of any kind.
package notifyrelay

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"regexp"
	"sync"
	"time"
)

// Web Push subscription key sizes, fixed by RFC 8291 (Message Encryption
// for Web Push): p256dh is an uncompressed P-256 public key
// (0x04 || X || Y), and auth is a 16-byte shared secret. These are
// structural facts about the Push API, duplicated locally from
// backend/internal/notifydevices' own identical constants rather than
// imported, exactly because this package must remain a fully independent
// leaf (see this package's own doc comment) -- the same
// duplicate-rather-than-import convention already used throughout the
// notify family (for example notifydelivery's own eventIDPattern, which
// duplicates alert_events.event_id's shape instead of importing
// internal/alerts).
const (
	p256dhDecodedLen = 65
	authDecodedLen   = 16

	// maxEndpointLen is a resource-safety ceiling against a pathological
	// caller-supplied string, not a claim about any real push service's
	// actual endpoint URL length. Matches notifydevices' own identical
	// ceiling.
	maxEndpointLen = 2048
	// maxTitleLen, maxBodyLen, and maxURLLen are resource-safety ceilings
	// on the minimal notification payload, chosen generously above any
	// realistic push-notification display length, never a claim about what
	// a specific provider or browser actually renders.
	maxTitleLen = 128
	maxBodyLen  = 512
	maxURLLen   = 2048
)

// Sentinel errors. None of these, nor any error this package returns, ever
// echoes the endpoint, key material, payload content, or any other
// caller-supplied value.
var (
	ErrInvalidOutboxID = errors.New("notifyrelay: outbox id is required")
	ErrInvalidEndpoint = errors.New("notifyrelay: endpoint must be an absolute https URL")
	ErrInvalidP256dh   = errors.New("notifyrelay: p256dh must be a base64url-encoded 65-byte P-256 public key")
	ErrInvalidAuth     = errors.New("notifyrelay: auth must be a base64url-encoded 16-byte secret")
	ErrInvalidTitle    = errors.New("notifyrelay: title is required and must not exceed the safe display length")
	ErrInvalidBody     = errors.New("notifyrelay: body must not exceed the safe display length")
	ErrInvalidURL      = errors.New("notifyrelay: url must be an absolute https URL")
	ErrInvalidCode     = errors.New("notifyrelay: result code contains unsupported characters or is too long")

	// ErrSenderUnavailable marks a Sender that cannot be used at all (for
	// example, a nil FakeSender). It is never returned for an ordinary
	// provider-facing outcome: a temporary failure, a permanent failure,
	// and an unauthorized/invalid subscription are all represented as a
	// successfully-returned Result (see Outcome's own doc comment), never
	// as this error.
	ErrSenderUnavailable = errors.New("notifyrelay: sender unavailable")
)

// codePattern duplicates notifydelivery's own errorCodePattern shape
// exactly (itself mirroring detection_audit.reason's convention): a fixed,
// safe, allow-listed-by-format-only code. Constraining Result.Code to this
// shape is a structural guarantee, not just a convention -- it makes it
// impossible for a Sender implementation to accidentally place a raw
// endpoint, key, payload fragment, or provider error string into a Result,
// since none of those shapes can ever match this pattern.
var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// OutboxID is a plain, caller-supplied identity carrier for one relay
// request. It is deliberately not notifyoutbox.OutboxID: see this
// package's own doc comment for why this package imports no other
// notify-family package. A future caller holding a notifyoutbox.OutboxID
// converts it explicitly (notifyrelay.OutboxID(entry.ID)) when constructing
// a Request.
type OutboxID int64

// decodeB64URL decodes a base64url string, accepting both the unpadded
// encoding every mainstream browser's PushSubscription.toJSON() produces
// and the padded variant, since padding presence is not a security
// property. Duplicates notifydevices' own identical helper (see this
// package's own doc comment on why).
func decodeB64URL(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

func validateAbsoluteHTTPSURL(raw string, maxLen int) bool {
	if raw == "" || len(raw) > maxLen {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// Payload is the minimal notification content a Sender relays: never a
// full transcript, a provider payload, or anything beyond what a push
// notification actually displays (docs/notification-relay-amendment-v1.md's
// own payload-minimization intent, Section 18).
type Payload struct {
	// Title is required: a push notification with no title is not a
	// meaningful request.
	Title string
	// Body is optional; empty means no body line.
	Body string
	// URL is the optional deep link a notification click should open.
	// Empty means no click destination.
	URL string
}

func (p Payload) validate() error {
	if p.Title == "" || len(p.Title) > maxTitleLen {
		return ErrInvalidTitle
	}
	if len(p.Body) > maxBodyLen {
		return ErrInvalidBody
	}
	if p.URL != "" && !validateAbsoluteHTTPSURL(p.URL, maxURLLen) {
		return ErrInvalidURL
	}
	return nil
}

// Request is one provider-neutral relay request: exactly the stable
// identity, the raw W3C Push API subscription values a future Web Push
// Sender would need (RFC 8291), and the minimal notification payload. It
// carries nothing a Sender implementation does not need: no user identity,
// no account state, no preference state, and no outbox/delivery-audit
// bookkeeping fields -- all of that is resolved by the caller (a future
// orchestrator) before a Request is ever constructed.
type Request struct {
	// OutboxID is the stable delivery/idempotency identity for this
	// request (see OutboxID's own doc comment). A Sender implementation is
	// expected to use it as a stable, retry-safe key -- for example, as
	// the seed for a deterministic idempotency key -- but this package
	// itself performs no such derivation.
	OutboxID OutboxID
	// Endpoint, P256dh, and Auth are exactly the W3C Push API subscription
	// object's own fields (PushSubscription.toJSON()), identical in shape
	// to notifydevices.Subscription -- duplicated, not imported, per this
	// package's own doc comment. Neither P256dh nor Auth is a provider
	// credential: both are opaque, per-subscription values a direct Web
	// Push Sender would need to encrypt a message to this specific browser
	// subscription.
	Endpoint string
	P256dh   string
	Auth     string
	Payload  Payload
	// TestMode is a plain, caller-supplied copy of the target device
	// registration's own notifydevices.Registration.TestMode value (Step
	// 8D-B Part 12B's design lock). It is an opaque bool this package
	// neither interprets nor validates against anything -- it exists so a
	// concrete Sender (which must never import notifydevices or any store,
	// per this package's own doc comment) can still enforce its own
	// environment/test-mode isolation policy using only information already
	// present on the Request, without gaining a dependency on the device
	// registration this value was copied from. A future caller converts it
	// explicitly (Request{TestMode: registration.TestMode, ...}), exactly
	// like OutboxID's own conversion convention.
	TestMode bool
}

// Validate checks every field of r independently. It never trusts a
// caller to have validated first, and never touches a network or a
// database.
func (r Request) Validate() error {
	if r.OutboxID <= 0 {
		return ErrInvalidOutboxID
	}
	if !validateAbsoluteHTTPSURL(r.Endpoint, maxEndpointLen) {
		return ErrInvalidEndpoint
	}
	if b, err := decodeB64URL(r.P256dh); err != nil || len(b) != p256dhDecodedLen {
		return ErrInvalidP256dh
	}
	if b, err := decodeB64URL(r.Auth); err != nil || len(b) != authDecodedLen {
		return ErrInvalidAuth
	}
	return r.Payload.validate()
}

// Outcome is exactly one of the four provider-neutral result classes below.
// There is no fifth value and no free-form outcome string: any concrete
// Sender implementation (direct Web Push, a provider REST client, or a
// fake) must map its own provider-specific responses onto one of these
// four, never invent a new class.
type Outcome string

const (
	// OutcomeAccepted means the target push service (or provider) accepted
	// the request for delivery. Per this design's own documented limits
	// (Step 8D-B Part 11 discovery), this proves only that the request was
	// accepted, never that a human actually saw the notification.
	OutcomeAccepted Outcome = "accepted"
	// OutcomeTemporaryFailure means the attempt did not succeed but may
	// succeed on a later retry (a timeout, a 5xx, a rate limit, or any
	// other ambiguous/transient response). A future caller is expected to
	// map this to a bounded-retry outcome, never an immediate tight-loop
	// retry.
	OutcomeTemporaryFailure Outcome = "temporary_failure"
	// OutcomePermanentFailure means the attempt failed in a way that will
	// never succeed on retry with the same request (a malformed payload,
	// a rejected configuration). A future caller is expected to map this
	// toward dead-letter/cancellation, never an indefinite retry.
	OutcomePermanentFailure Outcome = "permanent_failure"
	// OutcomeUnauthorized means the target subscription itself is no
	// longer valid (expired, revoked, or unknown to the push service --
	// for example an HTTP 404/410 from a real push service). A future
	// caller is expected to treat this as a signal to deactivate the
	// underlying device registration, never as a transient failure to
	// retry indefinitely (Section 8 of
	// docs/notification-relay-amendment-v1.md).
	OutcomeUnauthorized Outcome = "unauthorized"
)

// Valid reports whether o is one of the four approved values. An unknown
// value is always invalid, never silently accepted.
func (o Outcome) Valid() bool {
	switch o {
	case OutcomeAccepted, OutcomeTemporaryFailure, OutcomePermanentFailure, OutcomeUnauthorized:
		return true
	}
	return false
}

// Result is a Sender's provider-neutral answer to one Request. A non-nil
// error from Sender.Send means the attempt could not even be classified
// into one of the four Outcome values (a caller/Sender misconfiguration);
// a returned Result always carries a Valid Outcome and nothing else --
// never a raw provider error message, HTTP header, response body,
// endpoint, or key material.
type Result struct {
	Outcome Outcome
	// Code is an optional, fixed-shape, allow-listed-by-format-only
	// classification detail (see codePattern's own doc comment), mirroring
	// notifydelivery.Delivery.ErrorCode's identical convention exactly.
	// nil means no further detail. This package defines no closed set of
	// code values: a concrete Sender implementation is the producer, this
	// field only their generic, format-validating carrier.
	Code *string
	// RetryAfter is an optional, parsed backoff hint (RFC 9110 Section
	// 10.2.3's Retry-After header, in either its delta-seconds or
	// HTTP-date form), populated only when a concrete Sender's underlying
	// transport received one alongside a retryable response (for example
	// HTTP 429 or 5xx). nil means no such hint was available -- never a
	// claim that immediate retry is safe. This package performs no
	// parsing itself and imposes no bound on the value beyond it being
	// non-negative (see Validate); a concrete Sender is the only producer.
	// A future caller (orchestration) is never required to honor it, but
	// this field exists so the information is not silently discarded
	// before that caller exists.
	RetryAfter *time.Duration
}

// Validate checks that r carries a Valid Outcome, a non-negative
// RetryAfter if present, and, if present, a well-formed Code. A Sender
// implementation is expected to only ever return an already-valid Result;
// this exists so a caller (or a test) can verify that structurally rather
// than by trusting the implementation.
func (r Result) Validate() error {
	if !r.Outcome.Valid() {
		return errors.New("notifyrelay: result outcome is not one of the four approved values")
	}
	if r.Code != nil && !codePattern.MatchString(*r.Code) {
		return ErrInvalidCode
	}
	if r.RetryAfter != nil && *r.RetryAfter < 0 {
		return errors.New("notifyrelay: result retry-after must not be negative")
	}
	return nil
}

// Sender is the provider-neutral relay contract. A future, separately
// authorized concrete implementation (a direct Web Push client, per Step
// 8D-B Part 11A's discovery) satisfies this interface using server-held
// key material this package never sees or holds. This package declares
// only the contract: it never implements a real Sender itself.
type Sender interface {
	Send(ctx context.Context, req Request) (Result, error)
}

// FakeSender is a deterministic, in-memory Sender for tests. It performs
// no network I/O, holds no credential, contacts no real provider, and is
// never used by production code -- it exists solely so this package's own
// tests, and a future caller's tests, can exercise Sender-shaped code
// without any network capability. Safe for concurrent use.
type FakeSender struct {
	mu      sync.Mutex
	results map[OutboxID]Result
	errs    map[OutboxID]error
	calls   []Request
}

// NewFakeSender returns an empty FakeSender. With no configuration, Send
// returns (Result{Outcome: OutcomeAccepted}, nil) for any validly-shaped
// Request.
func NewFakeSender() *FakeSender {
	return &FakeSender{
		results: make(map[OutboxID]Result),
		errs:    make(map[OutboxID]error),
	}
}

// SetResult configures f to return result for every future Send call
// carrying this exact OutboxID, until changed again. Never mutates result
// itself, and never validates it: a test may deliberately configure an
// invalid Result to exercise a caller's own defensive handling.
func (f *FakeSender) SetResult(id OutboxID, result Result) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[id] = result
}

// SetError configures f to return err (instead of a Result) for every
// future Send call carrying this exact OutboxID, until changed again.
func (f *FakeSender) SetError(id OutboxID, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[id] = err
}

// Calls returns a copy of every Request this FakeSender has accepted so
// far, in call order. Mutating the returned slice never affects f.
func (f *FakeSender) Calls() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Request, len(f.calls))
	copy(out, f.calls)
	return out
}

// Send validates req exactly as a real Sender must, records it, and
// returns whatever this FakeSender was configured for that OutboxID (a
// fixed OutcomeAccepted Result by default). It never performs network I/O
// and never logs or otherwise exposes req's endpoint or key material.
func (f *FakeSender) Send(ctx context.Context, req Request) (Result, error) {
	if err := req.Validate(); err != nil {
		return Result{}, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, req)

	if err, ok := f.errs[req.OutboxID]; ok {
		return Result{}, err
	}
	if result, ok := f.results[req.OutboxID]; ok {
		return result, nil
	}
	return Result{Outcome: OutcomeAccepted}, nil
}
