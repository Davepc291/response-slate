// Typed request/response shapes matching backend/internal/authhttp's
// Step 9E /api/admin/users* JSON structures exactly. These interfaces
// mirror the Go structs field for field; they are not a redesign of the
// wire contract.

export interface AdminUserView {
  id: number;
  email: string;
  display_name: string;
  role: string;
  scope?: string;
  status: string;
  last_login_at?: string;
  created_at: string;
}

export interface AdminListUsersResponse {
  users: AdminUserView[];
  limit: number;
  offset: number;
}

export interface AdminInvitationCode {
  code: string;
  expires_at: string;
  sensitive: boolean;
  warning: string;
}

export interface AdminCreateUserRequest {
  email: string;
  display_name: string;
  role: string;
  scope: string;
}

export interface AdminCreateUserResponse {
  user: AdminUserView;
  invitation: AdminInvitationCode;
}

export interface AdminInvitationResponse {
  invitation: AdminInvitationCode;
}

export interface AdminChangeRoleRequest {
  role: string;
  scope: string;
}

export interface AdminRevokeSessionsResponse {
  revoked_count: number;
}

export interface AdminStatusResponse {
  status: string;
}

export interface AdminListUsersParams {
  scope?: string;
  role?: string;
  status?: string;
  search?: string;
  limit?: number;
  offset?: number;
}
