import { HttpErrorResponse, HttpInterceptorFn } from '@angular/common/http';
import { inject } from '@angular/core';
import { Router } from '@angular/router';
import { catchError, throwError } from 'rxjs';

import { ApiErrorEnvelope } from './auth-api.models';

const ADMIN_PATH_PREFIX = '/api/admin/';

/**
 * Step 9F-5/6 navigation-UX interceptor. When an authenticated
 * administrator's request to any /api/admin/* route is rejected with 403
 * mfa_verification_required (backend/internal/authhttp's
 * requireAdminMFAVerified — Section 5, AAX-07), this navigates to the
 * verification screen instead of leaving a generic forbidden message on
 * whichever admin screen made the call — one place covers every current and
 * future AdminApiService call, rather than repeating this check in each
 * admin component. This is convenience only, never the authorization
 * boundary the backend check already is: the error is always rethrown, so
 * every caller still receives the identical AdminResult failure
 * (admin-api.service.ts's own 'mfa_verification_required' mapping)
 * regardless of whether this interceptor also navigates.
 */
export const mfaRequiredInterceptor: HttpInterceptorFn = (req, next) => {
  // inject() only works synchronously while this interceptor function
  // itself is being invoked (an active injection context) — not inside
  // catchError's callback below, which runs later, asynchronously, once a
  // response/error arrives. Router is resolved here and captured by the
  // closure instead.
  const router = inject(Router);

  return next(req).pipe(
    catchError((error: unknown) => {
      if (error instanceof HttpErrorResponse && error.status === 403) {
        let pathname: string;
        try {
          pathname = new URL(req.url, window.location.origin).pathname;
        } catch {
          pathname = '';
        }
        const body = error.error as Partial<ApiErrorEnvelope> | null;
        if (pathname.startsWith(ADMIN_PATH_PREFIX) && body?.error?.code === 'mfa_verification_required') {
          void router.navigateByUrl(
            `/mobile/auth/mfa/verify?returnUrl=${encodeURIComponent(router.url)}`,
          );
        }
      }
      return throwError(() => error);
    }),
  );
};
