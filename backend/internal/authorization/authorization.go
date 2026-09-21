// Package authorization implements backend permission evaluation for the
// exact roles and scopes approved by
// docs/authentication-authorization-v1.md Section 6 (Roles and
// authorization). It is default-deny: an unknown role, an unknown
// permission, or a permission not listed for a role all deny, never fall
// through to an implicit allow. Angular visibility is never authorization
// (Section 6, Section 3): this package is the only thing a future protected
// HTTP handler may trust, and it has no dependency on any UI state.
package authorization

import "greenwich-fire-responder/backend/internal/identity"

// Permission is one of the exact operations Section 6's matrix names.
type Permission string

const (
	ViewProtectedCalls            Permission = "view_protected_calls"
	ViewUnitStatus                Permission = "view_unit_status"
	ViewTranscriptsEvidence       Permission = "view_transcripts_evidence"
	ManageUsers                   Permission = "manage_users"
	ChangeRoles                   Permission = "change_roles"
	SuspendDisableRestore         Permission = "suspend_disable_restore"
	ManageInvitationsResets       Permission = "manage_invitations_resets"
	RegisterNotificationDevice    Permission = "register_notification_device"
	ChangeNotificationPreferences Permission = "change_notification_preferences"
	ViewAuditHistory              Permission = "view_audit_history"
)

// Actor is the authenticated caller a permission check is evaluated for.
// Constructing one does not itself grant anything; only Allowed does.
type Actor struct {
	UserID identity.UserID
	Role   identity.Role
	Scope  identity.Scope
}

// reach classifies how a role's grant of a permission is bounded.
type reach int

const (
	reachNone   reach = iota // not granted; explicit for readability, same as absent
	reachGlobal              // unscoped: system_administrator, and read_only_auditor's read-only rows
	reachScoped              // requires actor.Scope == target scope (department_administrator, and
	// dispatcher_operator/responder's own-scope reads)
	reachSelf // requires the target identity to be the actor itself (notification
	// device/preference operations, for every role, per Section 6: "always
	// self-service for one's own identity only, never on another user's
	// behalf by any role in this generation")
)

// matrix is the authoritative encoding of Section 6's permission table.
// Simplifications from the proposed table, made because the underlying
// department/scope model (Section 15, item 3) and "own assignments" concept
// for responder are themselves unresolved contract questions:
//   - "own assignments" (responder's narrower view scope) is approximated as
//     "own scope," the same mechanism used for dispatcher_operator and
//     department_administrator, since no separate assignment model exists
//     yet.
//   - responder's "Limited/no" transcript/evidence access is resolved
//     conservatively to no access at all (default deny), consistent with
//     this package's overall default-deny posture: an ambiguous "maybe" in
//     the source table is never resolved to an allow by this package.
var matrix = map[identity.Role]map[Permission]reach{
	identity.RoleSystemAdministrator: {
		ViewProtectedCalls: reachGlobal, ViewUnitStatus: reachGlobal, ViewTranscriptsEvidence: reachGlobal,
		ManageUsers: reachGlobal, ChangeRoles: reachGlobal, SuspendDisableRestore: reachGlobal,
		ManageInvitationsResets: reachGlobal, ViewAuditHistory: reachGlobal,
		RegisterNotificationDevice: reachSelf, ChangeNotificationPreferences: reachSelf,
	},
	identity.RoleDepartmentAdministrator: {
		ViewProtectedCalls: reachScoped, ViewUnitStatus: reachScoped, ViewTranscriptsEvidence: reachScoped,
		ManageUsers: reachScoped, ChangeRoles: reachScoped, SuspendDisableRestore: reachScoped,
		ManageInvitationsResets: reachScoped, ViewAuditHistory: reachScoped,
		RegisterNotificationDevice: reachSelf, ChangeNotificationPreferences: reachSelf,
	},
	identity.RoleDispatcherOperator: {
		ViewProtectedCalls: reachScoped, ViewUnitStatus: reachScoped, ViewTranscriptsEvidence: reachScoped,
		RegisterNotificationDevice: reachSelf, ChangeNotificationPreferences: reachSelf,
	},
	identity.RoleResponder: {
		ViewProtectedCalls: reachScoped, ViewUnitStatus: reachScoped,
		RegisterNotificationDevice: reachSelf, ChangeNotificationPreferences: reachSelf,
	},
	identity.RoleReadOnlyAuditor: {
		ViewProtectedCalls: reachGlobal, ViewUnitStatus: reachGlobal, ViewTranscriptsEvidence: reachGlobal,
		ViewAuditHistory:           reachGlobal,
		RegisterNotificationDevice: reachSelf, ChangeNotificationPreferences: reachSelf,
	},
}

// Allowed reports whether actor may perform permission against a resource
// in targetScope, owned by targetUserID. Unused parameters for a given
// permission's reach are ignored (for example, targetUserID is irrelevant
// to a reachScoped permission). An unknown role, an unknown permission, or
// a permission absent from the role's entry all deny.
func Allowed(actor Actor, permission Permission, targetScope identity.Scope, targetUserID identity.UserID) bool {
	if !actor.Role.Valid() {
		return false
	}
	perms, ok := matrix[actor.Role]
	if !ok {
		return false
	}
	switch perms[permission] {
	case reachGlobal:
		return true
	case reachScoped:
		return actor.Scope != "" && actor.Scope == targetScope
	case reachSelf:
		return actor.UserID == targetUserID
	default:
		return false
	}
}

// CanAssignRole reports whether actor may create or change an account to
// newRole within targetScope, per Section 6's role table: only a
// system_administrator may ever assign an administrator role (including
// system_administrator itself), and a department_administrator may assign
// only a non-administrator role, only within its own scope.
func CanAssignRole(actor Actor, targetScope identity.Scope, newRole identity.Role) bool {
	if !newRole.Valid() {
		return false
	}
	switch actor.Role {
	case identity.RoleSystemAdministrator:
		return true
	case identity.RoleDepartmentAdministrator:
		if newRole.IsAdministrator() {
			return false
		}
		return actor.Scope != "" && actor.Scope == targetScope
	default:
		return false
	}
}
