// Package notifywebpush is the Step 8D-B Part 12 concrete, direct
// standards-based Web Push implementation of backend/internal/notifyrelay's
// Sender contract (RFC 8030/8291/8292), selected by Part 11A's discovery and
// locked by Part 12B's design review. It uses
// github.com/SherClockHolmes/webpush-go for the RFC 8291 message encryption
// and RFC 8292 VAPID JWT signing; this package itself implements no
// cryptography of its own.
//
// This package is disabled by default and fails closed at every boundary:
// New refuses to construct a usable Sender unless Options.Enabled is true
// and every other field is well-formed, and Send independently re-checks
// the stored Enabled state before doing anything else (Part 12B's locked
// design: the gate lives in both places, not just one). It never reads an
// environment variable itself -- Options is always caller-supplied,
// already-validated configuration; a future backend/internal/config wiring
// step (not this one) is responsible for turning GFR_NOTIFY_* environment
// variables into an Options value. It never generates a VAPID key pair,
// never holds a real credential, never runs inside a worker loop, never
// claims/mutates a notification_outbox row, never writes a
// notification_deliveries row, never revokes a device registration, and is
// never registered by backend/cmd/api: a caller (a test, or a future
// authorized orchestration component) must construct and invoke it
// explicitly, exactly like every other notify-family package in this step.
//
// This package deliberately depends on no notify-family sibling except
// notifyrelay itself (whose Sender interface it implements) and no store:
// Options.Env and Request.TestMode (Part 12B's own additive contract
// amendment) are plain, opaque values this package compares directly,
// never resolved by importing notifydevices or any database.
//
// Step 8D-B Part 13 corrected this package's own JSON wire encoding (see
// buildPushPayload's own doc comment): the plaintext payload sent is now
// shaped exactly as Angular's generated service worker requires in order to
// actually display a notification, instead of the flat shape Part 12
// shipped. notifyrelay.Payload itself is unchanged -- this is a wire-format
// concern local to this one concrete Sender.
package notifywebpush

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"greenwich-fire-responder/backend/internal/notifyrelay"
)

// VAPID key sizes. The public key is RFC 8291's uncompressed P-256 point
// (0x04 || X || Y), always exactly 65 bytes because crypto/elliptic's
// Marshal zero-pads both coordinates to a fixed width. The private key is
// NOT fixed-width: webpush-go's own GenerateVAPIDKeys encodes it via
// math/big's Int.Bytes(), which strips leading zero bytes -- a freshly,
// correctly generated 256-bit scalar is therefore usually 32 bytes but can
// legitimately be shorter (roughly a 1-in-256 chance per leading zero
// byte). Validating an exact 32-byte length would intermittently reject a
// perfectly valid key; only an upper bound is safe here.
const (
	vapidPublicKeyLen     = 65
	vapidPrivateKeyMaxLen = 32

	// maxSubjectLen is a resource-safety ceiling on the VAPID subject
	// (RFC 8292's "sub" claim: a mailto: address or an https URL), well
	// above any realistic contact address or URL length.
	maxSubjectLen = 320

	// TTLMin, TTLMax bound Options.TTL (RFC 8030's TTL header, seconds).
	// DefaultTTL (60s) is Step 8D-B Part 12B's locked recommendation: short
	// enough that a supplementary dispatch push is never delivered stale
	// long after its real relevance has passed, long enough to tolerate a
	// brief network handoff. The existing outbox bounded-retry/backoff
	// mechanism (Step 8D-B Part 6), not a long push-service TTL, is the
	// intended way to handle a device that is offline for longer than
	// this.
	TTLMin     = 30 * time.Second
	TTLMax     = 15 * time.Minute
	DefaultTTL = 60 * time.Second

	// SendTimeoutMin, SendTimeoutMax bound Options.SendTimeout (the
	// per-send bounded context timeout for the HTTP round trip).
	// DefaultSendTimeout mirrors backend/internal/transcription's own
	// established per-request timeout order of magnitude.
	SendTimeoutMin     = 1 * time.Second
	SendTimeoutMax     = 30 * time.Second
	DefaultSendTimeout = 10 * time.Second

	// maxDrainBytes bounds how much of a push service's response body this
	// package will ever read, even though that content is discarded
	// immediately and never logged (see drainAndClose) -- a resource-safety
	// ceiling against a pathological or misbehaving server, not a claim
	// about any real push service's actual response size.
	maxDrainBytes = 64 << 10
)

// Sentinel errors. Every one of these is a fixed string, never dynamically
// constructed from a caller-supplied or provider-supplied value: this
// package must never let an endpoint, key, subject, payload, or raw HTTP
// error text leak into an error or a log. Where an underlying operation
// (for example webpush-go's own SendNotificationWithContext, whose errors
// can embed the raw request URL via the standard library's *url.Error) could
// return a value-carrying error, this package deliberately discards it
// rather than wrapping or formatting it -- see Send's own comments at each
// such call site.
var (
	ErrRelayDisabled          = errors.New("notifywebpush: relay is disabled")
	ErrInvalidEnv             = errors.New("notifywebpush: environment must be exactly one of dev, preview, production")
	ErrInvalidVAPIDPublicKey  = errors.New("notifywebpush: vapid public key must be a base64url-encoded 65-byte uncompressed P-256 point")
	ErrInvalidVAPIDPrivateKey = errors.New("notifywebpush: vapid private key must be a base64url-encoded P-256 scalar of at most 32 bytes")
	ErrInvalidVAPIDSubject    = errors.New("notifywebpush: vapid subject must be a mailto: address or an absolute https URL")
	ErrInvalidTTL             = errors.New("notifywebpush: ttl is outside the configured bounded range")
	ErrInvalidUrgency         = errors.New("notifywebpush: urgency must be one of the four approved values")
	ErrInvalidSendTimeout     = errors.New("notifywebpush: send timeout is outside the configured bounded range")
	// ErrEnvironmentMismatch marks a Request whose TestMode does not match
	// the environment/test-mode pairing Step 8D-B Part 12B locked: dev and
	// preview may only ever address TestMode==true, production may only
	// ever address TestMode==false. It is returned before any HTTP
	// activity, and never carries the actual Env or TestMode value.
	ErrEnvironmentMismatch = errors.New("notifywebpush: environment/test-mode pairing is not permitted")
	// ErrPayloadEncoding marks an internal JSON-encoding failure for an
	// already-validated Payload. This should be unreachable in practice
	// (Request.Validate has already bounded every field), and exists only
	// so this path fails with a fixed sentinel rather than ever
	// propagating an encoding/json error that could otherwise echo payload
	// content.
	ErrPayloadEncoding = errors.New("notifywebpush: payload encoding failed")
)

// codePattern mirrors notifyrelay's own codePattern exactly: every
// Result.Code this package produces must match this fixed, safe,
// allow-listed-by-format-only shape, which structurally forbids embedding
// an endpoint, key, or response body fragment.
var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Env is exactly one of the three values below (Step 8D-B Section 20 /
// Part 12B's design lock). There is no fourth value.
type Env string

const (
	EnvDev        Env = "dev"
	EnvPreview    Env = "preview"
	EnvProduction Env = "production"
)

// Valid reports whether e is one of the three approved values.
func (e Env) Valid() bool {
	switch e {
	case EnvDev, EnvPreview, EnvProduction:
		return true
	}
	return false
}

// Urgency is exactly one of the four values RFC 8030 defines for the
// Urgency header. Defined locally (rather than reusing webpush.Urgency
// directly) so Options can be validated without depending on webpush-go's
// own validation, and so this package's public configuration surface does
// not change shape if the underlying library is ever replaced.
type Urgency string

const (
	UrgencyVeryLow Urgency = "very-low"
	UrgencyLow     Urgency = "low"
	UrgencyNormal  Urgency = "normal"
	UrgencyHigh    Urgency = "high"
)

// Valid reports whether u is one of the four approved values.
func (u Urgency) Valid() bool {
	switch u {
	case UrgencyVeryLow, UrgencyLow, UrgencyNormal, UrgencyHigh:
		return true
	}
	return false
}

// toLibrary converts u to webpush-go's own Urgency type. Safe by direct
// string-value correspondence: both enumerations use the identical four
// RFC 8030 header values.
func (u Urgency) toLibrary() webpush.Urgency {
	return webpush.Urgency(u)
}

// mailtoPattern is a deliberately simple, format-only check for the
// mailto: form of a VAPID subject (RFC 8292's "sub" claim): this package
// is not in the business of fully validating email address grammar, only
// of rejecting an obviously-wrong shape before it ever reaches a signed
// JWT.
var mailtoPattern = regexp.MustCompile(`^mailto:[^\s@]+@[^\s@]+\.[^\s@]+$`)

func validSubject(s string) bool {
	if len(s) == 0 || len(s) > maxSubjectLen {
		return false
	}
	if strings.HasPrefix(s, "https://") {
		u, err := url.Parse(s)
		return err == nil && u.Scheme == "https" && u.Host != ""
	}
	return mailtoPattern.MatchString(s)
}

// decodeVAPIDKey decodes a base64url VAPID key, accepting both the
// unpadded encoding webpush.GenerateVAPIDKeys produces and the padded
// variant. Duplicates notifyrelay's own identical decodeB64URL helper
// (this package's own doc comment explains why it depends on no other
// notify-family package besides notifyrelay).
func decodeVAPIDKey(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

// vapidSubscriberForLibrary strips a leading "mailto:" before handing the
// subject to webpush-go. webpush-go v1.4.0's own getVAPIDAuthorizationHeader
// unconditionally prepends "mailto:" to any subscriber string that does not
// itself start with "https:" -- passing an already-"mailto:"-prefixed
// subject through unchanged would double the prefix into
// "mailto:mailto:...". Confirmed by reading webpush-go v1.4.0's vapid.go
// source directly, not assumed.
func vapidSubscriberForLibrary(subject string) string {
	return strings.TrimPrefix(subject, "mailto:")
}

// Options is the complete, immutable configuration for a Sender. It is
// always caller-supplied, already-validated configuration: this package
// never calls os.Getenv itself (enforced by this package's own
// depcheck_test.go). A future backend/internal/config wiring step -- not
// this one -- is responsible for turning GFR_NOTIFY_* environment
// variables into an Options value.
type Options struct {
	// Enabled is the system-wide relay gate (GFR_NOTIFY_RELAY_ENABLED,
	// eventually). false is the only safe default: New refuses to
	// construct a Sender at all when this is false, and Send independently
	// re-checks it (Step 8D-B Part 12B's locked design).
	Enabled bool
	// Env selects which environment/test-mode pairing Send enforces (Step
	// 8D-B Section 20 / Part 12B's design lock).
	Env Env
	// VAPIDPublicKey and VAPIDPrivateKey are this server's own application
	// identity key pair (RFC 8292), base64url-encoded exactly as
	// webpush.GenerateVAPIDKeys produces them. Neither is a provider
	// credential in the OneSignal-style sense; both are still
	// security-sensitive server-held secrets.
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	// VAPIDSubject is RFC 8292's "sub" claim: a mailto: address or an
	// absolute https URL a push service can use to contact the operator of
	// this application server.
	VAPIDSubject string
	// TTL is RFC 8030's TTL header value, bounded to [TTLMin, TTLMax].
	// Never derived from an outbox entry's own expiry: notifyrelay.Request
	// deliberately carries no expiration information (Step 8D-B Part 12B's
	// design lock) -- this is a single fixed value for every send.
	TTL time.Duration
	// Urgency is RFC 8030's Urgency header value, fixed for every send in
	// this part (no dynamic per-alert urgency policy yet).
	Urgency Urgency
	// SendTimeout bounds the per-send HTTP round trip, bounded to
	// [SendTimeoutMin, SendTimeoutMax].
	SendTimeout time.Duration
}

func (o Options) validate() error {
	if !o.Enabled {
		return ErrRelayDisabled
	}
	if !o.Env.Valid() {
		return ErrInvalidEnv
	}
	pub, err := decodeVAPIDKey(o.VAPIDPublicKey)
	if err != nil || len(pub) != vapidPublicKeyLen || pub[0] != 0x04 {
		return ErrInvalidVAPIDPublicKey
	}
	priv, err := decodeVAPIDKey(o.VAPIDPrivateKey)
	if err != nil || len(priv) == 0 || len(priv) > vapidPrivateKeyMaxLen {
		return ErrInvalidVAPIDPrivateKey
	}
	if !validSubject(o.VAPIDSubject) {
		return ErrInvalidVAPIDSubject
	}
	if o.TTL < TTLMin || o.TTL > TTLMax {
		return ErrInvalidTTL
	}
	if !o.Urgency.Valid() {
		return ErrInvalidUrgency
	}
	if o.SendTimeout < SendTimeoutMin || o.SendTimeout > SendTimeoutMax {
		return ErrInvalidSendTimeout
	}
	return nil
}

// checkEnvironmentPairing enforces Step 8D-B Part 12B's locked symmetric
// rule before any HTTP activity: dev/preview may only ever address a
// TestMode==true Request; production may only ever address
// TestMode==false. It never returns a value carrying env or testMode.
func checkEnvironmentPairing(env Env, testMode bool) error {
	switch env {
	case EnvDev, EnvPreview:
		if !testMode {
			return ErrEnvironmentMismatch
		}
	case EnvProduction:
		if testMode {
			return ErrEnvironmentMismatch
		}
	default:
		return ErrInvalidEnv
	}
	return nil
}

// Sender is a concrete, direct Web Push notifyrelay.Sender. Its fields are
// unexported: the only way to obtain a usable *Sender is New, which is
// also the only place Options is validated -- there is no way to construct
// a *Sender that bypasses that validation.
type Sender struct {
	options Options
	client  *http.Client
}

// New validates o and returns a ready-to-use Sender, or refuses
// construction entirely (nil, error) if o.Enabled is false or any field is
// malformed -- fail closed, per Step 8D-B Part 12B's locked design. transport
// is used as the underlying http.RoundTripper; when nil, a fresh clone of
// http.DefaultTransport is used instead (preserving Go's normal proxy,
// connection-pooling, and TLS-verification defaults unchanged). Injecting a
// transport is intended for tests, so a local httptest.Server can be
// addressed without any real network reachability.
//
// The returned Sender always refuses to follow an HTTP redirect
// automatically (CheckRedirect returns http.ErrUseLastResponse): a real
// push service redirecting is not an expected, trusted behavior to follow
// silently, and RFC 8030 push services do not legitimately do so.
func New(o Options, transport http.RoundTripper) (*Sender, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}

	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return &Sender{options: o, client: client}, nil
}

// pushPayload, notificationOptions, notificationData, onActionClickMap, and
// onActionClickAction together produce the exact wire shape Angular's own
// generated service worker (ngsw-worker.js) requires in order to display a
// push notification and, on click, open a URL (Step 8D-B Part 13
// discovery/design-lock, verified directly against the shipped
// @angular/service-worker runtime source, not assumed): a top-level
// "notification" object (anything else -- including the flat
// {"title","body","url"} shape this package produced before Part 13 --
// causes ngsw-worker.js's own handlePush to silently return without ever
// calling showNotification, since it specifically checks
// `!data.notification || !data.notification.title`), with a click-time URL
// expressed only via notification.data.onActionClick.default.url (a bare
// top-level "url" field, inside or outside "notification", is never read by
// ngsw-worker.js's click handler and would just be silently dropped).
//
// This wire shape is deliberately owned by this package alone, not by
// notifyrelay.Payload: notifyrelay stays a generic, provider-neutral
// title/body/url carrier, and this concrete Web Push Sender is responsible
// for encoding that into whatever its one supported client (Angular's
// service worker) actually needs. It must never add a user id, event id,
// transcript, radio text, or any other alert metadata beyond the three
// fields notifyrelay.Payload already carries.
type pushPayload struct {
	Notification notificationOptions `json:"notification"`
}

type notificationOptions struct {
	Title string            `json:"title"`
	Body  string            `json:"body,omitempty"`
	Data  *notificationData `json:"data,omitempty"`
}

type notificationData struct {
	OnActionClick onActionClickMap `json:"onActionClick"`
}

type onActionClickMap struct {
	Default onActionClickAction `json:"default"`
}

type onActionClickAction struct {
	Operation string `json:"operation"`
	URL       string `json:"url"`
}

// buildPushPayload converts a provider-neutral notifyrelay.Payload into the
// Angular-service-worker-shaped plaintext this package encrypts and sends.
// Title is always present (notifyrelay.Request.Validate already requires a
// non-empty title before Send ever reaches this call). Body is omitted
// entirely when empty (via its own omitempty), and the whole
// data/onActionClick structure is omitted entirely when URL is empty --
// never emitted as an empty/null placeholder either way.
func buildPushPayload(p notifyrelay.Payload) pushPayload {
	notification := notificationOptions{Title: p.Title, Body: p.Body}
	if p.URL != "" {
		notification.Data = &notificationData{
			OnActionClick: onActionClickMap{
				Default: onActionClickAction{Operation: "openWindow", URL: p.URL},
			},
		}
	}
	return pushPayload{Notification: notification}
}

// Send implements notifyrelay.Sender. It refuses (fail closed) before any
// HTTP activity if this Sender is disabled, if req is not well-formed, or
// if req.TestMode does not match this Sender's own Env under Step 8D-B
// Part 12B's locked pairing rule. A network/transport-level failure and
// every recognized or unrecognized push-service HTTP response are always
// classified into a valid notifyrelay.Result (see classifyResponse); Send
// returns a non-nil error only for the three refusal cases above, never for
// an ordinary provider-facing outcome.
func (s *Sender) Send(ctx context.Context, req notifyrelay.Request) (notifyrelay.Result, error) {
	if s == nil || !s.options.Enabled {
		// Defense in depth: Options.Enabled is already refused by New, and
		// Sender's fields are unexported so nothing outside this package
		// can construct one that bypasses New. This re-check enforces the
		// invariant structurally rather than relying solely on New's own
		// gate (Step 8D-B Part 12B's locked design).
		return notifyrelay.Result{}, ErrRelayDisabled
	}
	if err := req.Validate(); err != nil {
		return notifyrelay.Result{}, err
	}
	if err := checkEnvironmentPairing(s.options.Env, req.TestMode); err != nil {
		return notifyrelay.Result{}, err
	}

	sendCtx, cancel := context.WithTimeout(ctx, s.options.SendTimeout)
	defer cancel()

	body, err := json.Marshal(buildPushPayload(req.Payload))
	if err != nil {
		return notifyrelay.Result{}, ErrPayloadEncoding
	}

	sub := &webpush.Subscription{
		Endpoint: req.Endpoint,
		Keys:     webpush.Keys{P256dh: req.P256dh, Auth: req.Auth},
	}
	wpOptions := &webpush.Options{
		HTTPClient:      s.client,
		Subscriber:      vapidSubscriberForLibrary(s.options.VAPIDSubject),
		TTL:             int(s.options.TTL.Seconds()),
		Urgency:         s.options.Urgency.toLibrary(),
		VAPIDPublicKey:  s.options.VAPIDPublicKey,
		VAPIDPrivateKey: s.options.VAPIDPrivateKey,
	}

	resp, err := webpush.SendNotificationWithContext(sendCtx, body, sub, wpOptions)
	if err != nil {
		// err is deliberately never inspected, wrapped, or logged: the
		// standard library's own *url.Error (the common shape of a
		// transport-level failure here) embeds the raw request URL --
		// req.Endpoint -- in its Error() string. Every network/transport
		// failure is classified identically regardless of its underlying
		// cause (see this package's own response-mapping doc).
		return resultWithCode(notifyrelay.OutcomeTemporaryFailure, "network_error"), nil
	}
	defer drainAndClose(resp.Body)

	return classifyResponse(resp), nil
}

// resultWithCode builds a Result with an optional, format-validated Code.
// Every call site in this package passes a fixed string literal, never a
// value derived from a response body, header, or request field.
func resultWithCode(outcome notifyrelay.Outcome, code string) notifyrelay.Result {
	if code == "" {
		return notifyrelay.Result{Outcome: outcome}
	}
	c := code
	if !codePattern.MatchString(c) {
		// Unreachable for this package's own fixed literals; fails closed
		// rather than ever returning a malformed Code if that invariant is
		// ever broken by a future edit.
		return notifyrelay.Result{Outcome: outcome}
	}
	return notifyrelay.Result{Outcome: outcome, Code: &c}
}

func resultWithRetryAfter(result notifyrelay.Result, retryAfter *time.Duration) notifyrelay.Result {
	result.RetryAfter = retryAfter
	return result
}

// classifyResponse maps a push service's raw HTTP response onto exactly
// one of notifyrelay's four provider-neutral Outcome values, per Step
// 8D-B Part 12B's locked response-mapping table (rechecked against RFC
// 8030 and realistic push-service behavior):
//
//   - 201/202: accepted (RFC 8030's own defined success codes).
//   - any other 2xx: accepted, Code "unexpected_success_status" -- HTTP
//     semantics already guarantee 2xx means the server processed the
//     request; classifying it as a failure would trigger a needless retry
//     and risk a real duplicate delivery.
//   - 3xx: permanent_failure, Code "unexpected_redirect_status" -- this
//     Sender's transport never follows a redirect automatically, and a
//     real push service does not legitimately redirect.
//   - 400: permanent_failure, Code "malformed_request".
//   - 401: permanent_failure, Code "vapid_auth_absent" -- this Sender's
//     own credential/config problem, never the target device's.
//   - 403: permanent_failure, Code "vapid_auth_rejected" -- same
//     reasoning as 401.
//   - 404/410: unauthorized, Code "subscription_gone" -- RFC 8030 defines
//     404 for an expired subscription; 410 is the conventional real-world
//     code for a permanently-gone one. A future caller is expected to
//     treat this as a signal to deactivate the device registration.
//   - 413: permanent_failure, Code "payload_too_large".
//   - 429: temporary_failure, Code "rate_limited", RetryAfter parsed if
//     present.
//   - other 4xx: permanent_failure, Code "unrecognized_client_error" --
//     matches general HTTP semantics: a 4xx means the request itself was
//     rejected, so the safe default is to not blindly retry it unmodified.
//   - 5xx (recognized or not): temporary_failure, Code
//     "unrecognized_server_error" for the unrecognized case, RetryAfter
//     parsed if present -- safe because retries are already bounded by the
//     existing outbox attempt ceiling, never unbounded.
//   - anything else entirely unparseable: temporary_failure, Code
//     "unrecognized_status" -- a conservative fallback; bounded retry is
//     the safer failure mode than silently dropping a real alert on an
//     unrecognized transport anomaly.
func classifyResponse(resp *http.Response) notifyrelay.Result {
	retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusAccepted:
		return notifyrelay.Result{Outcome: notifyrelay.OutcomeAccepted}
	case http.StatusBadRequest:
		return resultWithCode(notifyrelay.OutcomePermanentFailure, "malformed_request")
	case http.StatusUnauthorized:
		return resultWithCode(notifyrelay.OutcomePermanentFailure, "vapid_auth_absent")
	case http.StatusForbidden:
		return resultWithCode(notifyrelay.OutcomePermanentFailure, "vapid_auth_rejected")
	case http.StatusNotFound, http.StatusGone:
		return resultWithCode(notifyrelay.OutcomeUnauthorized, "subscription_gone")
	case http.StatusRequestEntityTooLarge:
		return resultWithCode(notifyrelay.OutcomePermanentFailure, "payload_too_large")
	case http.StatusTooManyRequests:
		return resultWithRetryAfter(resultWithCode(notifyrelay.OutcomeTemporaryFailure, "rate_limited"), retryAfter)
	}

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return resultWithCode(notifyrelay.OutcomeAccepted, "unexpected_success_status")
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return resultWithCode(notifyrelay.OutcomePermanentFailure, "unexpected_redirect_status")
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return resultWithCode(notifyrelay.OutcomePermanentFailure, "unrecognized_client_error")
	case resp.StatusCode >= 500 && resp.StatusCode < 600:
		return resultWithRetryAfter(resultWithCode(notifyrelay.OutcomeTemporaryFailure, "unrecognized_server_error"), retryAfter)
	default:
		return resultWithCode(notifyrelay.OutcomeTemporaryFailure, "unrecognized_status")
	}
}

// parseRetryAfter parses an RFC 9110 Section 10.2.3 Retry-After header
// value in either its delta-seconds or HTTP-date form, relative to
// time.Now(). An empty, malformed, or negative delta-seconds header
// returns nil -- this package never guesses a retry delay it cannot
// safely parse.
func parseRetryAfter(header string) *time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil
	}
	if seconds, err := strconv.ParseUint(header, 10, 32); err == nil {
		d := time.Duration(seconds) * time.Second
		return &d
	}
	if t, err := http.ParseTime(header); err == nil {
		d := time.Until(t)
		if d < 0 {
			d = 0
		}
		return &d
	}
	return nil
}

// drainAndClose bounds how much of resp's body this package will ever
// read, then closes it (required for the underlying transport to reuse
// the connection). The content is always discarded: a push service
// response body is never logged, persisted, or placed into a Result,
// Code, or error -- it carries no content this package trusts or needs.
func drainAndClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxDrainBytes))
	_ = body.Close()
}
