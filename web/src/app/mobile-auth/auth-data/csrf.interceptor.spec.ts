import { HttpClient, provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';

import { csrfInterceptor } from './csrf.interceptor';

// jsdom's test origin is http://localhost/, and a real browser never stores
// a `__Host-` prefixed (or `Secure`) cookie over plain HTTP — so setting
// document.cookie directly in a test would silently no-op and prove
// nothing. Stubbing the getter exercises exactly what this interceptor
// reads, independent of jsdom's own cookie-jar/secure-context behavior.
function mockCookieHeader(value: string): void {
  vi.spyOn(document, 'cookie', 'get').mockReturnValue(value);
}

describe('csrfInterceptor', () => {
  let http: HttpClient;
  let httpMock: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(withInterceptors([csrfInterceptor])),
        provideHttpClientTesting(),
      ],
    });
    http = TestBed.inject(HttpClient);
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    httpMock.verify();
    vi.restoreAllMocks();
  });

  it('attaches X-CSRF-Token on POST /api/auth/logout when the cookie is present', () => {
    mockCookieHeader('__Host-gfr_csrf=csrf-value-123');
    http.post('/api/auth/logout', {}).subscribe();
    const req = httpMock.expectOne('/api/auth/logout');
    expect(req.request.headers.get('X-CSRF-Token')).toBe('csrf-value-123');
    req.flush({ status: 'logged_out' });
  });

  it('attaches X-CSRF-Token on DELETE /api/auth/sessions/{id}', () => {
    mockCookieHeader('__Host-gfr_csrf=csrf-value-456');
    http.delete('/api/auth/sessions/7').subscribe();
    const req = httpMock.expectOne('/api/auth/sessions/7');
    expect(req.request.headers.get('X-CSRF-Token')).toBe('csrf-value-456');
    req.flush({ status: 'revoked' });
  });

  it('does not attach the header to GET requests', () => {
    mockCookieHeader('__Host-gfr_csrf=csrf-value-789');
    http.get('/api/auth/me').subscribe();
    const req = httpMock.expectOne('/api/auth/me');
    expect(req.request.headers.has('X-CSRF-Token')).toBe(false);
    req.flush({});
  });

  it('does not attach the header to POST /api/auth/login (pre-session, backend does not require it)', () => {
    mockCookieHeader('__Host-gfr_csrf=csrf-value-abc');
    http.post('/api/auth/login', {}).subscribe();
    const req = httpMock.expectOne('/api/auth/login');
    expect(req.request.headers.has('X-CSRF-Token')).toBe(false);
    req.flush({});
  });

  it('never attaches the CSRF value to a cross-origin request, even for a state-changing method', () => {
    mockCookieHeader('__Host-gfr_csrf=csrf-value-secret');
    http.post('https://evil.example.com/api/auth/logout', {}).subscribe({ error: () => {} });
    const req = httpMock.expectOne('https://evil.example.com/api/auth/logout');
    expect(req.request.headers.has('X-CSRF-Token')).toBe(false);
    req.flush(null, { status: 0, statusText: 'blocked' });
  });

  it('forwards the request unmodified when no CSRF cookie is readable', () => {
    mockCookieHeader('');
    http.post('/api/auth/logout-all', {}).subscribe();
    const req = httpMock.expectOne('/api/auth/logout-all');
    expect(req.request.headers.has('X-CSRF-Token')).toBe(false);
    req.flush({ status: 'logged_out_everywhere' });
  });

  it('reads only the readable CSRF cookie, never the HttpOnly session cookie value', () => {
    mockCookieHeader('__Host-gfr_session=should-never-be-read; __Host-gfr_csrf=csrf-value-999');
    http.post('/api/auth/logout', {}).subscribe();
    const req = httpMock.expectOne('/api/auth/logout');
    expect(req.request.headers.get('X-CSRF-Token')).toBe('csrf-value-999');
    req.flush({ status: 'logged_out' });
  });

  it('attaches X-CSRF-Token on POST /api/admin/users (create)', () => {
    mockCookieHeader('__Host-gfr_csrf=csrf-value-admin-1');
    http.post('/api/admin/users', {}).subscribe();
    const req = httpMock.expectOne('/api/admin/users');
    expect(req.request.headers.get('X-CSRF-Token')).toBe('csrf-value-admin-1');
    req.flush({});
  });

  it('attaches X-CSRF-Token on POST /api/admin/users/{id}/suspend', () => {
    mockCookieHeader('__Host-gfr_csrf=csrf-value-admin-2');
    http.post('/api/admin/users/42/suspend', {}).subscribe();
    const req = httpMock.expectOne('/api/admin/users/42/suspend');
    expect(req.request.headers.get('X-CSRF-Token')).toBe('csrf-value-admin-2');
    req.flush({});
  });

  it('does not attach the header to GET /api/admin/users', () => {
    mockCookieHeader('__Host-gfr_csrf=csrf-value-admin-3');
    http.get('/api/admin/users').subscribe();
    const req = httpMock.expectOne('/api/admin/users');
    expect(req.request.headers.has('X-CSRF-Token')).toBe(false);
    req.flush({});
  });

  it('ignores a same-named cookie value pair that only prefix-matches the session cookie', () => {
    // Guards against a naive substring/startsWith cookie parse that could
    // accidentally read part of __Host-gfr_session as if it were the CSRF
    // cookie's value.
    mockCookieHeader('__Host-gfr_sessionish=not-the-real-session');
    http.post('/api/auth/logout', {}).subscribe();
    const req = httpMock.expectOne('/api/auth/logout');
    expect(req.request.headers.has('X-CSRF-Token')).toBe(false);
    req.flush({ status: 'logged_out' });
  });
});
