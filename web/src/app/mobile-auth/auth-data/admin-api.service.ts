import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable, catchError, map, of } from 'rxjs';

import {
  AdminChangeRoleRequest,
  AdminCreateUserRequest,
  AdminCreateUserResponse,
  AdminInvitationResponse,
  AdminListUsersParams,
  AdminListUsersResponse,
  AdminRevokeSessionsResponse,
  AdminStatusResponse,
  AdminUserView,
} from './admin-api.models';

/**
 * Safe, UI-facing failure categories for the Step 9E administrator API.
 * Every backend error envelope is mapped into one of these; no raw backend
 * message, status code, or field ever reaches a template directly from an
 * HTTP error, mirroring AuthApiService's identical convention.
 */
export type AdminErrorKind =
  | 'not_authenticated'
  | 'forbidden'
  | 'not_found'
  | 'email_conflict'
  | 'invalid_state'
  | 'invalid_input'
  | 'self_action_forbidden'
  | 'session_expired'
  | 'rate_limited'
  | 'unavailable';

export interface AdminFailure {
  readonly kind: AdminErrorKind;
  readonly message: string;
}

export type AdminResult<T> =
  { readonly ok: true; readonly value: T } | { readonly ok: false; readonly error: AdminFailure };

const GENERIC_UNAVAILABLE: AdminFailure = {
  kind: 'unavailable',
  message: 'This action is temporarily unavailable.',
};

// Maps backend error codes (backend/internal/authhttp's writeAdminError
// "code" field) to a safe, contract-approved UI message. An unknown code
// falls back to the generic unavailable message rather than surfacing
// internal backend detail.
const CODE_TO_FAILURE: Readonly<Record<string, AdminFailure>> = {
  not_authenticated: { kind: 'not_authenticated', message: 'Sign-in required.' },
  forbidden: { kind: 'forbidden', message: 'You are not authorized to perform this action.' },
  not_found: { kind: 'not_found', message: 'User not found.' },
  email_conflict: { kind: 'email_conflict', message: 'This email already has an account.' },
  invalid_state: {
    kind: 'invalid_state',
    message: "This action is not permitted for the account's current state.",
  },
  invalid_input: { kind: 'invalid_input', message: 'Check the submitted values and try again.' },
  self_action_forbidden: {
    kind: 'self_action_forbidden',
    message: 'You cannot perform this action on your own account.',
  },
  rate_limited: { kind: 'rate_limited', message: 'Too many attempts. Try again later.' },
  // A missing/expired CSRF cookie or a rejected origin both indicate the
  // caller no longer has a usable session context; both fold into the same
  // safe "sign in again" state, mirroring AuthApiService.
  csrf_validation_failed: {
    kind: 'session_expired',
    message: 'Your session has expired. Please sign in again.',
  },
  origin_not_allowed: {
    kind: 'session_expired',
    message: 'Your session has expired. Please sign in again.',
  },
};

/**
 * Typed Angular client for the Step 9E administrator user-management API
 * (backend/internal/authhttp's /api/admin/users* routes). Every call uses a
 * same-origin relative URL and `withCredentials: true`; the existing Step
 * 9D CSRF interceptor (auth-data/csrf.interceptor.ts) attaches the
 * X-CSRF-Token header to every state-changing call here automatically. No
 * response from this service is ever cached by this client (backend sends
 * Cache-Control: no-store; this service adds no caching layer of its own).
 * The raw invitation/reset code in AdminCreateUserResponse /
 * AdminInvitationResponse exists only in the Observable value handed to the
 * calling component; this service never persists it anywhere.
 */
@Injectable({ providedIn: 'root' })
export class AdminApiService {
  private readonly http = inject(HttpClient);

  listUsers(params: AdminListUsersParams = {}): Observable<AdminResult<AdminListUsersResponse>> {
    const query = new URLSearchParams();
    if (params.scope) query.set('scope', params.scope);
    if (params.role) query.set('role', params.role);
    if (params.status) query.set('status', params.status);
    if (params.search) query.set('search', params.search);
    if (params.limit != null) query.set('limit', String(params.limit));
    if (params.offset != null) query.set('offset', String(params.offset));
    const qs = query.toString();
    return this.get<AdminListUsersResponse>(`/api/admin/users${qs ? `?${qs}` : ''}`);
  }

  getUser(id: number): Observable<AdminResult<AdminUserView>> {
    return this.get<AdminUserView>(`/api/admin/users/${id}`);
  }

  createUser(request: AdminCreateUserRequest): Observable<AdminResult<AdminCreateUserResponse>> {
    return this.post<AdminCreateUserResponse>('/api/admin/users', request);
  }

  resendInvitation(id: number): Observable<AdminResult<AdminInvitationResponse>> {
    return this.post<AdminInvitationResponse>(`/api/admin/users/${id}/resend-invitation`, {});
  }

  suspend(id: number): Observable<AdminResult<AdminStatusResponse>> {
    return this.post<AdminStatusResponse>(`/api/admin/users/${id}/suspend`, {});
  }

  disable(id: number): Observable<AdminResult<AdminStatusResponse>> {
    return this.post<AdminStatusResponse>(`/api/admin/users/${id}/disable`, {});
  }

  restore(id: number): Observable<AdminResult<AdminStatusResponse>> {
    return this.post<AdminStatusResponse>(`/api/admin/users/${id}/restore`, {});
  }

  revokeSessions(id: number): Observable<AdminResult<AdminRevokeSessionsResponse>> {
    return this.post<AdminRevokeSessionsResponse>(`/api/admin/users/${id}/revoke-sessions`, {});
  }

  resetCredential(id: number): Observable<AdminResult<AdminInvitationResponse>> {
    return this.post<AdminInvitationResponse>(`/api/admin/users/${id}/reset-credential`, {});
  }

  changeRole(
    id: number,
    request: AdminChangeRoleRequest,
  ): Observable<AdminResult<AdminStatusResponse>> {
    return this.post<AdminStatusResponse>(`/api/admin/users/${id}/role`, request);
  }

  private post<T>(url: string, body: unknown): Observable<AdminResult<T>> {
    return this.http.post<T>(url, body, { withCredentials: true }).pipe(
      map((value): AdminResult<T> => ({ ok: true, value })),
      catchError((error: unknown) => of(this.toFailureResult<T>(error))),
    );
  }

  private get<T>(url: string): Observable<AdminResult<T>> {
    return this.http.get<T>(url, { withCredentials: true }).pipe(
      map((value): AdminResult<T> => ({ ok: true, value })),
      catchError((error: unknown) => of(this.toFailureResult<T>(error))),
    );
  }

  private toFailureResult<T>(error: unknown): AdminResult<T> {
    return { ok: false, error: this.toFailure(error) };
  }

  private toFailure(error: unknown): AdminFailure {
    if (!(error instanceof HttpErrorResponse)) {
      return GENERIC_UNAVAILABLE;
    }
    if (error.status === 0) {
      // No HTTP response reached the browser at all (offline, DNS failure,
      // connection refused, CORS preflight rejection): treated identically
      // to a backend-reported 503, never distinguished for the user, and
      // never queued for later retry (Step 9E requirement: "fail closed").
      return GENERIC_UNAVAILABLE;
    }
    const body = error.error as { error?: { code?: string } } | null;
    const code = body?.error?.code;
    if (code && Object.prototype.hasOwnProperty.call(CODE_TO_FAILURE, code)) {
      return CODE_TO_FAILURE[code];
    }
    return GENERIC_UNAVAILABLE;
  }
}
