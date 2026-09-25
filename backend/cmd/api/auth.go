package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"greenwich-fire-responder/backend/internal/adminservice"
	"greenwich-fire-responder/backend/internal/authhttp"
	"greenwich-fire-responder/backend/internal/config"
	"greenwich-fire-responder/backend/internal/identityauditstore"
	"greenwich-fire-responder/backend/internal/identityservice"
	"greenwich-fire-responder/backend/internal/identitystore"
	"greenwich-fire-responder/backend/internal/mfa"
	"greenwich-fire-responder/backend/internal/passwordpolicy"
	"greenwich-fire-responder/backend/internal/session"
)

const authConnectTimeout = 5 * time.Second

// buildAuthHandlers wires the Step 9C authentication HTTP API into a
// dedicated PostgreSQL connection pool, only when cfg.Auth.Enabled — an
// existing deployment that has not set GFR_AUTH_ENABLED is completely
// unaffected (see also config.Load, which already refuses to enable
// authentication without a configured database URL). It never seeds an
// administrator, a default account, or any credential: this function only
// opens a connection and constructs handlers around the existing,
// unmodified migration 000008 schema, which itself inserts zero rows.
//
// A dedicated pool is opened here (rather than reusing the *database.DB the
// rest of this program uses) because identitystore.Postgres and
// identityauditstore.Postgres need pgx's richer *pgxpool.Pool surface
// (transactions via Begin), which database.DB deliberately does not expose
// outside its own package. This function does not use
// identitystore.Open/identityauditstore.Open, whose localhost-only
// connection guard is a Step 9B test/local-development convenience never
// intended to gate a real deployment's DATABASE_URL.
func buildAuthHandlers(ctx context.Context, cfg config.Config, logger *slog.Logger) (*authhttp.Handlers, func(), error) {
	if !cfg.Auth.Enabled {
		return nil, func() {}, nil
	}
	// Validate before any network I/O: an invalid configuration must fail
	// startup immediately, never spend time attempting a database
	// connection first (Section 7: "Invalid duration, cookie, origin, or
	// authentication configuration must fail startup").
	if err := cfg.Auth.Validate(); err != nil {
		return nil, nil, err
	}

	connectCtx, cancel := context.WithTimeout(ctx, authConnectTimeout)
	defer cancel()

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, nil, errors.New("invalid authentication database configuration")
	}
	if poolCfg.ConnConfig.ConnectTimeout <= 0 || poolCfg.ConnConfig.ConnectTimeout > authConnectTimeout {
		poolCfg.ConnConfig.ConnectTimeout = authConnectTimeout
	}
	poolCfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(connectCtx, poolCfg)
	if err != nil {
		return nil, nil, errors.New("authentication database unavailable")
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, nil, errors.New("authentication database unavailable")
	}

	store := &identitystore.Postgres{DB: pool}
	auditStore := &identityauditstore.Postgres{DB: pool}
	svc := identityservice.New(store, auditStore, identityservice.Config{
		Session:          session.Config{IdleTimeout: cfg.Auth.SessionIdleTimeout, AbsoluteLifetime: cfg.Auth.SessionMaxLifetime},
		PasswordResetTTL: cfg.Auth.PasswordResetTTL,
		PasswordPolicy:   passwordpolicy.DefaultPolicy(),
		HashParams:       passwordpolicy.DefaultParams(),
	}, logger)

	handlers, err := authhttp.New(svc, cfg.Auth, logger)
	if err != nil {
		pool.Close()
		return nil, nil, err
	}

	// Step 9F-6: the WebAuthn passkey provider is constructed, and wired
	// into svc, only when GFR_AUTH_MFA_RP_ID (and its required companion,
	// GFR_AUTH_MFA_RP_DISPLAY_NAME) is set — authconfig.Options.Validate
	// already rejects one being set without the other, so both are known
	// non-empty here whenever MFARPID is. Every deployment that has not set
	// these keeps identityservice.Service.SetMFAProvider entirely uncalled,
	// leaving /api/auth/mfa/* exactly as functionally inert (503) as it was
	// before this step (see mfa.go's own doc comment). RPOrigins reuses the
	// exact same AllowedOrigins already validated for the CSRF Origin
	// check, rather than a second, independently-configured origin list —
	// see authconfig.Options.MFARPID's doc comment for why.
	if cfg.Auth.MFARPID != "" {
		provider, err := mfa.NewProvider(mfa.Config{
			RPID:          cfg.Auth.MFARPID,
			RPDisplayName: cfg.Auth.MFARPDisplayName,
			RPOrigins:     cfg.Auth.AllowedOrigins,
		})
		if err != nil {
			pool.Close()
			return nil, nil, err
		}
		svc.SetMFAProvider(provider)
	}

	// Step 9E: the administrator user-management API is wired up under the
	// identical cfg.Auth.Enabled gate as everything else in this function —
	// it never seeds an administrator, so it is reachable only once some
	// separate, out-of-band process provisions the first one (see
	// identitystore's integration test fixtures for how that is done in a
	// test-only context).
	adminSvc := adminservice.New(store, auditStore, adminservice.Config{
		InvitationTTL: cfg.Auth.InvitationTTL,
	}, logger)
	handlers.SetAdmin(adminSvc)

	return handlers, pool.Close, nil
}
