import { HttpInterceptorFn } from '@angular/common/http';

// The companion, JavaScript-readable CSRF cookie set by
// backend/internal/authcookie (never the HttpOnly __Host-gfr_session
// cookie, which this file never reads or references).
const CSRF_COOKIE_NAME = '__Host-gfr_csrf';
const CSRF_HEADER_NAME = 'X-CSRF-Token';
const STATE_CHANGING_METHODS = new Set(['POST', 'PUT', 'PATCH', 'DELETE']);

// The exact set of same-origin /api/auth routes backend/internal/authhttp's
// Mux wraps with requireCSRF. Login, first-time-login, and both
// password-reset routes are pre-session and are NOT wrapped there, so this
// interceptor deliberately does not attach a header for them either — an
// unnecessary header would do nothing useful and this list exists so the
// interceptor's behavior can be verified against the backend route table
// directly, not "every mutating request".
function requiresCsrfHeader(pathname: string, method: string): boolean {
  if (!STATE_CHANGING_METHODS.has(method)) {
    return false;
  }
  if (pathname === '/api/auth/logout' || pathname === '/api/auth/logout-all') {
    return true;
  }
  return pathname.startsWith('/api/auth/sessions/');
}

function readCsrfCookie(): string | null {
  const raw = typeof document !== 'undefined' ? document.cookie : '';
  if (!raw) {
    return null;
  }
  for (const entry of raw.split('; ')) {
    const separator = entry.indexOf('=');
    if (separator === -1) {
      continue;
    }
    if (entry.slice(0, separator) === CSRF_COOKIE_NAME) {
      try {
        return decodeURIComponent(entry.slice(separator + 1));
      } catch {
        return null;
      }
    }
  }
  return null;
}

/**
 * Attaches the X-CSRF-Token header to same-origin, state-changing
 * /api/auth requests that the backend requires it on (Step 9C CSRF
 * defense). It never attaches the header to a cross-origin request, a
 * GET/HEAD request, or a request outside the exact backend-required route
 * set, and it never logs the CSRF value. A missing or unreadable CSRF
 * cookie forwards the request unmodified; the backend then rejects it with
 * `csrf_validation_failed`, which AuthApiService maps to a safe
 * session-expired UI state rather than this interceptor guessing.
 */
export const csrfInterceptor: HttpInterceptorFn = (req, next) => {
  let target: URL;
  try {
    target = new URL(req.url, window.location.origin);
  } catch {
    return next(req);
  }

  if (target.origin !== window.location.origin) {
    return next(req);
  }
  if (!requiresCsrfHeader(target.pathname, req.method)) {
    return next(req);
  }

  const csrfValue = readCsrfCookie();
  if (!csrfValue) {
    return next(req);
  }

  return next(req.clone({ setHeaders: { [CSRF_HEADER_NAME]: csrfValue } }));
};
