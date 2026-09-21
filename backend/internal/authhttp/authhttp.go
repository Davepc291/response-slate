// Package authhttp is the Step 9C authentication HTTP transport layer built
// on top of backend/internal/identityservice. It implements the exact
// routes and response rules docs/authentication-authorization-v1.md
// Section 11.3 proposes (narrowed and, in one place, extended — see this
// package's Routes doc comment for the exact, reported deviations), JSON
// request/response handling, the secure session cookie, CSRF protection,
// session-resolving/authorization middleware, and a bounded rate-limiting
// seam. It never accepts or emits a password, raw token, cookie value,
// password hash, or internal error detail in a response body.
package authhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"greenwich-fire-responder/backend/internal/adminservice"
	"greenwich-fire-responder/backend/internal/authconfig"
	"greenwich-fire-responder/backend/internal/authcookie"
	"greenwich-fire-responder/backend/internal/authcsrf"
	"greenwich-fire-responder/backend/internal/authratelimit"
	"greenwich-fire-responder/backend/internal/clientip"
	"greenwich-fire-responder/backend/internal/identity"
	"greenwich-fire-responder/backend/internal/identityservice"
	"greenwich-fire-responder/backend/internal/session"
)

// maxRequestBodyBytes bounds every authentication request body. Every
// payload this API accepts (an email, a password, a device label) is small;
// this is generous headroom, not an attempt to support large payloads.
const maxRequestBodyBytes = 16 * 1024

// Handlers holds every dependency the authentication HTTP surface needs.
// Construct with New; the zero value is not usable.
type Handlers struct {
	svc   *identityservice.Service
	admin *adminservice.Service

	csrf           authcsrf.Deriver
	allowedOrigins map[string]bool
	ipResolver     clientip.Resolver

	loginPerAccount *authratelimit.MemoryLimiter
	loginPerIP      *authratelimit.MemoryLimiter
	invitation      *authratelimit.MemoryLimiter
	resetRequest    *authratelimit.MemoryLimiter
	resetComplete   *authratelimit.MemoryLimiter

	logger *slog.Logger
	clock  func() time.Time
}

// New constructs Handlers from a running Service and validated
// authconfig.Options. It returns an error rather than panicking because,
// unlike this package's other constructors, Options is expected to be
// loaded from process environment at real startup, and an operator
// configuration mistake must fail cleanly (Section 7's "invalid... auth
// configuration must fail startup").
func New(svc *identityservice.Service, opts authconfig.Options, logger *slog.Logger) (*Handlers, error) {
	if svc == nil {
		return nil, errors.New("authhttp: service is required")
	}
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	if !opts.Enabled {
		return nil, errors.New("authhttp: authentication is not enabled")
	}
	csrfDeriver, err := authcsrf.NewDeriver(opts.CSRFSecret)
	if err != nil {
		return nil, err
	}
	origins := make(map[string]bool, len(opts.AllowedOrigins))
	for _, o := range opts.AllowedOrigins {
		origins[o] = true
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Handlers{
		svc:             svc,
		csrf:            csrfDeriver,
		allowedOrigins:  origins,
		ipResolver:      clientip.NewResolver(opts.TrustedProxies),
		loginPerAccount: authratelimit.NewMemoryLimiter(opts.LoginRateLimitPerAccount.ToLimiterOptions()),
		loginPerIP:      authratelimit.NewMemoryLimiter(opts.LoginRateLimitPerIP.ToLimiterOptions()),
		invitation:      authratelimit.NewMemoryLimiter(opts.InvitationRateLimit.ToLimiterOptions()),
		resetRequest:    authratelimit.NewMemoryLimiter(opts.PasswordResetRequestRateLimit.ToLimiterOptions()),
		resetComplete:   authratelimit.NewMemoryLimiter(opts.PasswordResetCompleteRateLimit.ToLimiterOptions()),
		logger:          logger,
		clock:           func() time.Time { return time.Now().UTC() },
	}, nil
}

// Principal is the minimal authenticated identity attached to a request's
// context by requireSession. It never carries a password hash or raw
// token.
type Principal struct {
	UserID      identity.UserID
	SessionID   session.ID
	Role        identity.Role
	Scope       identity.Scope
	Email       string
	DisplayName string
	Status      identity.AccountState
}

type contextKey int

const (
	ctxPrincipal contextKey = iota
	ctxSessionToken
)

// PrincipalFromContext retrieves the authenticated principal a prior call
// to requireSession attached to ctx.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxPrincipal).(Principal)
	return p, ok
}

// SetAdmin attaches the Step 9E administrator user-management service and
// registers its routes the next time Mux is called. Admin routes are
// completely absent from the served mux until this is called: an existing
// caller of New that never calls SetAdmin (every Step 9C/9D test, and any
// deployment that has not separately wired up adminservice) is entirely
// unaffected, matching this repository's additive-rollout convention.
func (h *Handlers) SetAdmin(svc *adminservice.Service) {
	h.admin = svc
}

// --- JSON envelope and request-body handling -------------------------------

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

// setCommonHeaders applies the headers required on every authentication
// response (Section 2: "Cache-Control: no-store and appropriate related
// headers").
func setCommonHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Pragma", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Type", "application/json")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	setCommonHeaders(w)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: errorBody{Code: code, Message: message}})
}

// decodeJSON enforces strict Content-Type, a bounded body, and strict,
// single-object JSON decoding with unknown fields rejected. It writes its
// own error response and returns false on any failure, so callers can
// simply `if !decodeJSON(...) { return }`.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json.")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request body is too large.")
			return false
		}
		writeError(w, http.StatusBadRequest, "malformed_json", "Request body is not valid JSON.")
		return false
	}
	// A second JSON value in the same body (whether a duplicate top-level
	// document or trailing garbage) is rejected rather than silently
	// ignored. Duplicate *keys* within a single JSON object follow Go's
	// standard, deterministic last-value-wins decoding; this package does
	// not additionally token-scan for that narrower case.
	if dec.More() {
		writeError(w, http.StatusBadRequest, "malformed_json", "Request body must contain exactly one JSON value.")
		return false
	}
	return true
}

func (h *Handlers) respondLocked(w http.ResponseWriter) {
	writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many attempts. Try again later.")
}

func (h *Handlers) respondUnavailable(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable, "unavailable", "Sign-in is temporarily unavailable.")
}

func (h *Handlers) setSessionCookies(w http.ResponseWriter, rawToken string, expiresAt time.Time) {
	http.SetCookie(w, authcookie.NewSessionCookie(rawToken, expiresAt))
	digest := session.Digest(rawToken)
	http.SetCookie(w, authcookie.NewCSRFCookie(h.csrf.Derive(digest), expiresAt))
}

func (h *Handlers) clearSessionCookies(w http.ResponseWriter) {
	http.SetCookie(w, authcookie.ClearSessionCookie())
	http.SetCookie(w, authcookie.ClearCSRFCookie())
}
