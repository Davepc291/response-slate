import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable, catchError, map, of } from 'rxjs';

import {
  ApiErrorEnvelope,
  FirstTimeLoginRequest,
  LoginRequest,
  MeResponse,
  MessageResponse,
  PasswordResetCompleteBody,
  PasswordResetRequestBody,
  SessionView,
  StatusResponse,
} from './auth-api.models';
import { MFAEnrollBeginResponse, MFAVerifyBeginResponse } from './webauthn.models';

/**
 * Safe, UI-facing failure categories. Every backend error envelope is
 * mapped into one of these; no raw backend message, status code, or field
 * ever reaches a template directly from an HTTP error.
 */
export type AuthErrorKind =
  | 'invalid_credentials'
  | 'account_suspended'
  | 'invitation_expired'
  | 'invitation_invalid'
  | 'reset_link_expired'
  | 'reset_link_invalid'
  | 'password_too_weak'
  | 'password_breached'
  | 'rate_limited'
  | 'not_authenticated'
  | 'session_expired'
  | 'mfa_not_eligible'
  | 'mfa_ceremony_invalid'
  | 'mfa_response_invalid'
  | 'unavailable';

export interface AuthFailure {
  readonly kind: AuthErrorKind;
  readonly message: string;
}

export type AuthResult<T> =
  { readonly ok: true; readonly value: T } | { readonly ok: false; readonly error: AuthFailure };

const GENERIC_UNAVAILABLE: AuthFailure = {
  kind: 'unavailable',
  message: 'Sign-in is temporarily unavailable.',
};

// Maps backend error codes (backend/internal/authhttp's writeError "code"
// field) to a safe, contract-approved UI message. An unknown code falls
// back to the generic unavailable message rather than surfacing internal
// backend detail.
const CODE_TO_FAILURE: Readonly<Record<string, AuthFailure>> = {
  invalid_credentials: {
    kind: 'invalid_credentials',
    message: 'That email or password is incorrect.',
  },
  account_suspended: {
    kind: 'account_suspended',
    message: 'This account is suspended. Contact your administrator.',
  },
  rate_limited: { kind: 'rate_limited', message: 'Too many attempts. Try again later.' },
  invitation_expired: {
    kind: 'invitation_expired',
    message: 'This invitation has expired. Ask your administrator to resend it.',
  },
  invitation_invalid: {
    kind: 'invitation_invalid',
    message: 'This invitation link is invalid or has already been used.',
  },
  reset_link_expired: {
    kind: 'reset_link_expired',
    message: 'This password reset link has expired. Request a new one.',
  },
  reset_link_invalid: {
    kind: 'reset_link_invalid',
    message: 'This password reset link is invalid or has already been used.',
  },
  password_too_weak: { kind: 'password_too_weak', message: 'Choose a longer password.' },
  password_breached: {
    kind: 'password_breached',
    message: 'This password has appeared in a data breach. Choose a different one.',
  },
  not_authenticated: { kind: 'not_authenticated', message: 'Sign-in required.' },
  // Shared by both /api/auth/mfa/enroll and /api/auth/mfa/verify (Step
  // 9F-5): each backend route uses this identical code for a different
  // underlying reason (no eligible account state to enroll; no enrolled
  // passkey to verify), so this message is written generically enough to be
  // safe on either screen rather than distinguishing them.
  mfa_not_eligible: {
    kind: 'mfa_not_eligible',
    message: 'This account cannot complete this passkey step right now.',
  },
  mfa_ceremony_invalid: {
    kind: 'mfa_ceremony_invalid',
    message: 'That took too long. Start this step again.',
  },
  mfa_response_invalid: {
    kind: 'mfa_response_invalid',
    message: 'That passkey could not be verified. Try again.',
  },
  // A missing/expired CSRF cookie or a rejected origin both indicate the
  // caller no longer has a usable session context; both fold into the same
  // safe "sign in again" state rather than distinguishing the cause.
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
 * Typed Angular client for the Step 9C authentication API
 * (backend/internal/authhttp). Every call uses a same-origin relative
 * `/api/auth/...` URL and `withCredentials: true` so the browser attaches
 * the HttpOnly session cookie; the cookie value itself is never read here.
 * Every response and error is converted into an `AuthResult`, so a caller
 * never touches an `HttpErrorResponse` or a raw backend body directly.
 */
@Injectable({ providedIn: 'root' })
export class AuthApiService {
  private readonly http = inject(HttpClient);

  login(request: LoginRequest): Observable<AuthResult<MeResponse>> {
    return this.post<MeResponse>('/api/auth/login', request);
  }

  firstTimeLogin(request: FirstTimeLoginRequest): Observable<AuthResult<StatusResponse>> {
    return this.post<StatusResponse>('/api/auth/first-time-login', request);
  }

  requestPasswordReset(request: PasswordResetRequestBody): Observable<AuthResult<MessageResponse>> {
    return this.post<MessageResponse>('/api/auth/password-reset', request);
  }

  completePasswordReset(
    request: PasswordResetCompleteBody,
  ): Observable<AuthResult<StatusResponse>> {
    return this.post<StatusResponse>('/api/auth/password-reset/complete', request);
  }

  me(): Observable<AuthResult<MeResponse>> {
    return this.get<MeResponse>('/api/auth/me');
  }

  sessions(): Observable<AuthResult<SessionView[]>> {
    return this.get<SessionView[]>('/api/auth/sessions');
  }

  logout(): Observable<AuthResult<StatusResponse>> {
    return this.post<StatusResponse>('/api/auth/logout', {});
  }

  logoutAll(): Observable<AuthResult<StatusResponse>> {
    return this.post<StatusResponse>('/api/auth/logout-all', {});
  }

  /**
   * Begins a WebAuthn registration ceremony (Step 9F-3). Reachable either
   * with a normal session (adding another passkey) or, for a
   * password_change_required administrator, with the narrow
   * __Host-gfr_mfa_enroll bridging cookie the browser attaches
   * automatically — this call never reads or references either cookie's
   * value directly.
   */
  mfaEnrollBegin(): Observable<AuthResult<MFAEnrollBeginResponse>> {
    return this.post<MFAEnrollBeginResponse>('/api/auth/mfa/enroll', { action: 'begin' });
  }

  /** Completes a WebAuthn registration ceremony with the browser's raw response, converted to JSON by auth-data/webauthn.ts. */
  mfaEnrollFinish(credential: unknown): Observable<AuthResult<StatusResponse>> {
    return this.post<StatusResponse>('/api/auth/mfa/enroll', { action: 'finish', credential });
  }

  /** Begins a WebAuthn authentication ceremony (Step 9F-4) against the caller's current, already-established session. */
  mfaVerifyBegin(): Observable<AuthResult<MFAVerifyBeginResponse>> {
    return this.post<MFAVerifyBeginResponse>('/api/auth/mfa/verify', { action: 'begin' });
  }

  /** Completes a WebAuthn authentication ceremony, marking the current session MFA-verified on success. */
  mfaVerifyFinish(credential: unknown): Observable<AuthResult<StatusResponse>> {
    return this.post<StatusResponse>('/api/auth/mfa/verify', { action: 'finish', credential });
  }

  revokeSession(id: number): Observable<AuthResult<StatusResponse>> {
    return this.http
      .delete<StatusResponse>(`/api/auth/sessions/${id}`, { withCredentials: true })
      .pipe(
        map((value): AuthResult<StatusResponse> => ({ ok: true, value })),
        catchError((error: unknown) => of(this.toFailureResult<StatusResponse>(error))),
      );
  }

  private post<T>(url: string, body: unknown): Observable<AuthResult<T>> {
    return this.http.post<T>(url, body, { withCredentials: true }).pipe(
      map((value): AuthResult<T> => ({ ok: true, value })),
      catchError((error: unknown) => of(this.toFailureResult<T>(error))),
    );
  }

  private get<T>(url: string): Observable<AuthResult<T>> {
    return this.http.get<T>(url, { withCredentials: true }).pipe(
      map((value): AuthResult<T> => ({ ok: true, value })),
      catchError((error: unknown) => of(this.toFailureResult<T>(error))),
    );
  }

  private toFailureResult<T>(error: unknown): AuthResult<T> {
    return { ok: false, error: this.toFailure(error) };
  }

  private toFailure(error: unknown): AuthFailure {
    if (!(error instanceof HttpErrorResponse)) {
      return GENERIC_UNAVAILABLE;
    }
    if (error.status === 0) {
      // No HTTP response reached the browser at all (offline, DNS failure,
      // connection refused, CORS preflight rejection): treated identically
      // to a backend-reported 503, never distinguished for the user.
      return GENERIC_UNAVAILABLE;
    }
    const body = error.error as Partial<ApiErrorEnvelope> | null;
    const code = body?.error?.code;
    if (code && Object.prototype.hasOwnProperty.call(CODE_TO_FAILURE, code)) {
      return CODE_TO_FAILURE[code];
    }
    return GENERIC_UNAVAILABLE;
  }
}
