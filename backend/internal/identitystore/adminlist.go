package identitystore

import (
	"context"
	"strings"

	"greenwich-fire-responder/backend/internal/identity"
)

// defaultListLimit and maxListLimit bound every admin user-listing query
// (Step 9E requirement: "Pagination and filters must be bounded"). A caller
// requesting more than maxListLimit is silently clamped, never rejected,
// matching this repository's existing bounded-limit convention elsewhere
// (see transcriptreview.Postgres.Queue/History).
const (
	defaultListLimit = 25
	maxListLimit     = 100
	maxSearchLen     = 200
)

// ListUsersFilter carries every bound the caller (adminservice) applies to
// an administrator user listing. Scope is a plain equality filter, not an
// authorization check: adminservice is responsible for pinning Scope to a
// department administrator's own scope before this filter ever reaches this
// package, exactly as ChangeRole's doc comment states this package performs
// no authorization check itself.
type ListUsersFilter struct {
	// Scope restricts results to exactly this scope when non-nil. A nil
	// Scope means no scope restriction (every account, regardless of
	// scope) — only ever safe for a system administrator caller.
	Scope *identity.Scope
	Role  *identity.Role
	// Status must be one of identity's valid account states when non-nil.
	Status *identity.AccountState
	// Search is matched as a case-insensitive prefix against the
	// normalized email and a case-insensitive substring against the
	// display name. Empty means no text filter. LIKE metacharacters in
	// Search are escaped before use, so a caller-supplied '%' or '_'
	// never changes the query's matching behavior.
	Search string
	Limit  int
	Offset int
}

func escapeLike(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(s)
}

func (f ListUsersFilter) normalized() ListUsersFilter {
	out := f
	if out.Limit <= 0 {
		out.Limit = defaultListLimit
	}
	if out.Limit > maxListLimit {
		out.Limit = maxListLimit
	}
	if out.Offset < 0 {
		out.Offset = 0
	}
	if len(out.Search) > maxSearchLen {
		out.Search = out.Search[:maxSearchLen]
	}
	out.Search = strings.TrimSpace(out.Search)
	return out
}

// ListUsers returns a bounded, ordered page of accounts visible under
// filter. It performs no authorization check itself (see this package's
// ChangeRole doc comment for the same convention): the caller must have
// already resolved and authorized filter.Scope for the requesting
// administrator.
func (p *Postgres) ListUsers(ctx context.Context, filter ListUsersFilter) ([]identity.User, error) {
	if p == nil || p.DB == nil {
		return nil, ErrUnavailable
	}
	f := filter.normalized()

	var scope *string
	if f.Scope != nil {
		s := string(*f.Scope)
		scope = &s
	}
	var role *string
	if f.Role != nil {
		r := string(*f.Role)
		role = &r
	}
	var status *string
	if f.Status != nil {
		s := string(*f.Status)
		status = &s
	}
	var search *string
	if f.Search != "" {
		s := escapeLike(strings.ToLower(f.Search))
		search = &s
	}

	rows, err := p.DB.Query(ctx, `SELECT `+userSelectColumns+` FROM users
        WHERE ($1::text IS NULL OR scope = $1)
          AND ($2::text IS NULL OR role = $2)
          AND ($3::text IS NULL OR status = $3)
          AND ($4::text IS NULL OR lower(normalized_email) LIKE $4 || '%' ESCAPE '\' OR lower(display_name) LIKE '%' || $4 || '%' ESCAPE '\')
        ORDER BY id
        LIMIT $5 OFFSET $6`,
		scope, role, status, search, f.Limit, f.Offset)
	if err != nil {
		return nil, safeDB(err)
	}
	defer rows.Close()

	var out []identity.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, safeDB(err)
	}
	return out, nil
}
