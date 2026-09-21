package authorization

import (
	"testing"

	"greenwich-fire-responder/backend/internal/identity"
)

func TestUnknownRoleDeniesEverything(t *testing.T) {
	actor := Actor{UserID: 1, Role: identity.Role("bogus"), Scope: "engine-2"}
	for _, p := range []Permission{ViewProtectedCalls, ManageUsers, ViewAuditHistory, RegisterNotificationDevice} {
		if Allowed(actor, p, "engine-2", 1) {
			t.Errorf("unknown role must deny permission %s", p)
		}
	}
}

func TestUnknownPermissionDenies(t *testing.T) {
	actor := Actor{UserID: 1, Role: identity.RoleSystemAdministrator, Scope: ""}
	if Allowed(actor, Permission("made_up_permission"), "", 1) {
		t.Error("unknown permission must deny even for system_administrator")
	}
}

func TestSystemAdministratorGlobalReach(t *testing.T) {
	actor := Actor{UserID: 1, Role: identity.RoleSystemAdministrator}
	for _, p := range []Permission{ViewProtectedCalls, ViewUnitStatus, ViewTranscriptsEvidence,
		ManageUsers, ChangeRoles, SuspendDisableRestore, ManageInvitationsResets, ViewAuditHistory} {
		if !Allowed(actor, p, "any-other-scope", 999) {
			t.Errorf("system_administrator must have global reach for %s", p)
		}
	}
}

func TestSystemAdministratorCannotActOnAnotherUsersNotificationSettings(t *testing.T) {
	actor := Actor{UserID: 1, Role: identity.RoleSystemAdministrator}
	if Allowed(actor, RegisterNotificationDevice, "", 2) {
		t.Error("no role, including system_administrator, may register a notification device for another user")
	}
	if Allowed(actor, ChangeNotificationPreferences, "", 2) {
		t.Error("no role, including system_administrator, may change another user's notification preferences")
	}
	if !Allowed(actor, RegisterNotificationDevice, "", 1) {
		t.Error("system_administrator must be able to register its own device")
	}
}

func TestDepartmentAdministratorScopedReach(t *testing.T) {
	actor := Actor{UserID: 2, Role: identity.RoleDepartmentAdministrator, Scope: "engine-2"}
	if !Allowed(actor, ManageUsers, "engine-2", 0) {
		t.Error("department_administrator must manage users within its own scope")
	}
	if Allowed(actor, ManageUsers, "engine-3", 0) {
		t.Error("department_administrator must not manage users outside its own scope")
	}
	if Allowed(actor, ManageUsers, "", 0) {
		t.Error("department_administrator must not manage users in an unscoped (global) request")
	}
}

func TestDepartmentAdministratorWithoutScopeDeniesScoped(t *testing.T) {
	actor := Actor{UserID: 2, Role: identity.RoleDepartmentAdministrator, Scope: ""}
	if Allowed(actor, ManageUsers, "", 0) {
		t.Error("a department_administrator with no assigned scope must not match an unscoped target (fail closed)")
	}
}

func TestDispatcherOperatorScopedViewOnly(t *testing.T) {
	actor := Actor{UserID: 3, Role: identity.RoleDispatcherOperator, Scope: "engine-2"}
	if !Allowed(actor, ViewProtectedCalls, "engine-2", 0) {
		t.Error("dispatcher_operator must view protected calls within its own scope")
	}
	if Allowed(actor, ViewProtectedCalls, "engine-3", 0) {
		t.Error("dispatcher_operator must not view protected calls outside its own scope")
	}
	if Allowed(actor, ManageUsers, "engine-2", 0) {
		t.Error("dispatcher_operator must never manage users")
	}
	if Allowed(actor, ViewAuditHistory, "engine-2", 0) {
		t.Error("dispatcher_operator must never view audit history")
	}
}

func TestResponderNarrowestViewAccess(t *testing.T) {
	actor := Actor{UserID: 4, Role: identity.RoleResponder, Scope: "engine-2"}
	if !Allowed(actor, ViewProtectedCalls, "engine-2", 0) {
		t.Error("responder must view protected calls within its own scope")
	}
	if Allowed(actor, ViewTranscriptsEvidence, "engine-2", 0) {
		t.Error("responder's ambiguous 'limited/no' transcript access must resolve to deny (default deny)")
	}
	if Allowed(actor, ManageUsers, "engine-2", 0) {
		t.Error("responder must never manage users")
	}
}

func TestReadOnlyAuditorGlobalReadNoMutation(t *testing.T) {
	actor := Actor{UserID: 5, Role: identity.RoleReadOnlyAuditor}
	if !Allowed(actor, ViewProtectedCalls, "any-scope", 0) {
		t.Error("read_only_auditor must have global read access to protected calls")
	}
	if !Allowed(actor, ViewAuditHistory, "any-scope", 0) {
		t.Error("read_only_auditor must have global audit history access")
	}
	for _, p := range []Permission{ManageUsers, ChangeRoles, SuspendDisableRestore, ManageInvitationsResets} {
		if Allowed(actor, p, "any-scope", 0) {
			t.Errorf("read_only_auditor must never be granted mutation permission %s", p)
		}
	}
	if !Allowed(actor, RegisterNotificationDevice, "", 5) {
		t.Error("read_only_auditor must still be able to register its own notification device")
	}
}

func TestEveryRoleSelfServiceNotificationOnly(t *testing.T) {
	roles := []identity.Role{
		identity.RoleSystemAdministrator, identity.RoleDepartmentAdministrator,
		identity.RoleDispatcherOperator, identity.RoleResponder, identity.RoleReadOnlyAuditor,
	}
	for _, r := range roles {
		actor := Actor{UserID: 10, Role: r, Scope: "engine-2"}
		if !Allowed(actor, ChangeNotificationPreferences, "", 10) {
			t.Errorf("%s must be able to change its own notification preferences", r)
		}
		if Allowed(actor, ChangeNotificationPreferences, "", 11) {
			t.Errorf("%s must not be able to change another user's notification preferences", r)
		}
	}
}

func TestCanAssignRole(t *testing.T) {
	sysadmin := Actor{UserID: 1, Role: identity.RoleSystemAdministrator}
	if !CanAssignRole(sysadmin, "any-scope", identity.RoleSystemAdministrator) {
		t.Error("system_administrator must be able to create another system_administrator")
	}

	deptadmin := Actor{UserID: 2, Role: identity.RoleDepartmentAdministrator, Scope: "engine-2"}
	if CanAssignRole(deptadmin, "engine-2", identity.RoleSystemAdministrator) {
		t.Error("department_administrator must never assign system_administrator")
	}
	if CanAssignRole(deptadmin, "engine-2", identity.RoleDepartmentAdministrator) {
		t.Error("department_administrator must never assign department_administrator (non-administrator roles only)")
	}
	if !CanAssignRole(deptadmin, "engine-2", identity.RoleDispatcherOperator) {
		t.Error("department_administrator must be able to assign a non-administrator role within its own scope")
	}
	if CanAssignRole(deptadmin, "engine-3", identity.RoleDispatcherOperator) {
		t.Error("department_administrator must not assign roles outside its own scope")
	}

	dispatcher := Actor{UserID: 3, Role: identity.RoleDispatcherOperator, Scope: "engine-2"}
	if CanAssignRole(dispatcher, "engine-2", identity.RoleResponder) {
		t.Error("dispatcher_operator must never assign any role")
	}

	if CanAssignRole(sysadmin, "any-scope", identity.Role("bogus")) {
		t.Error("assigning an unknown role must always be denied")
	}
}
