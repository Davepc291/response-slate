import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';

import { AuthResult } from './auth-api.service';
import { AuthApiService } from './auth-api.service';
import { MeResponse } from './auth-api.models';

describe('AuthApiService', () => {
  let service: AuthApiService;
  let httpMock: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    service = TestBed.inject(AuthApiService);
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    httpMock.verify();
  });

  it('logs in against the exact relative same-origin URL with credentials included', () => {
    let captured: AuthResult<MeResponse> | undefined;
    service
      .login({ email: 'a@example.com', password: 'correct horse battery staple' })
      .subscribe((r) => {
        captured = r;
      });

    const req = httpMock.expectOne('/api/auth/login');
    expect(req.request.method).toBe('POST');
    expect(req.request.withCredentials).toBe(true);
    expect(req.request.body).toEqual({
      email: 'a@example.com',
      password: 'correct horse battery staple',
    });

    const response: MeResponse = {
      user_id: 1,
      email: 'a@example.com',
      display_name: 'A',
      role: 'responder',
      status: 'active',
    };
    req.flush(response);

    expect(captured).toEqual({ ok: true, value: response });
  });

  it('maps an invalid_credentials error envelope to the generic UI failure', () => {
    let captured: AuthResult<MeResponse> | undefined;
    service.login({ email: 'a@example.com', password: 'wrong' }).subscribe((r) => {
      captured = r;
    });

    httpMock
      .expectOne('/api/auth/login')
      .flush(
        { error: { code: 'invalid_credentials', message: 'internal backend detail' } },
        { status: 401, statusText: 'Unauthorized' },
      );

    expect(captured).toEqual({
      ok: false,
      error: { kind: 'invalid_credentials', message: 'That email or password is incorrect.' },
    });
  });

  it('never surfaces the raw backend message for an unrecognized error code', () => {
    let captured: AuthResult<MeResponse> | undefined;
    service.login({ email: 'a@example.com', password: 'x' }).subscribe((r) => {
      captured = r;
    });

    httpMock.expectOne('/api/auth/login').flush(
      {
        error: {
          code: 'some_internal_db_failure',
          message: 'pq: connection refused at 10.0.0.5',
        },
      },
      { status: 500, statusText: 'Internal Server Error' },
    );

    expect(captured).toEqual({
      ok: false,
      error: { kind: 'unavailable', message: 'Sign-in is temporarily unavailable.' },
    });
    if (captured && !captured.ok) {
      expect(captured.error.message).not.toContain('pq:');
      expect(captured.error.message).not.toContain('10.0.0.5');
    }
  });

  it('maps a network-level failure (status 0) to the generic unavailable state', () => {
    let captured: AuthResult<MeResponse> | undefined;
    service.me().subscribe((r) => {
      captured = r;
    });
    httpMock.expectOne('/api/auth/me').error(new ProgressEvent('error'), { status: 0 });
    expect(captured).toEqual({
      ok: false,
      error: { kind: 'unavailable', message: 'Sign-in is temporarily unavailable.' },
    });
  });

  it('issues GET /api/auth/me and /api/auth/sessions with credentials included', () => {
    service.me().subscribe();
    const meReq = httpMock.expectOne('/api/auth/me');
    expect(meReq.request.method).toBe('GET');
    expect(meReq.request.withCredentials).toBe(true);
    meReq.flush({
      user_id: 1,
      email: 'a@example.com',
      display_name: 'A',
      role: 'responder',
      status: 'active',
    });

    service.sessions().subscribe();
    const sessionsReq = httpMock.expectOne('/api/auth/sessions');
    expect(sessionsReq.request.method).toBe('GET');
    expect(sessionsReq.request.withCredentials).toBe(true);
    sessionsReq.flush([]);
  });

  it('revokes a session with DELETE against the exact id-scoped URL', () => {
    service.revokeSession(42).subscribe();
    const req = httpMock.expectOne('/api/auth/sessions/42');
    expect(req.request.method).toBe('DELETE');
    expect(req.request.withCredentials).toBe(true);
    req.flush({ status: 'revoked' });
  });

  it('sends the invitation token only inside the JSON body of first-time-login', () => {
    service
      .firstTimeLogin({ token: 'invite-token-value', password: 'a long passphrase here' })
      .subscribe();
    const req = httpMock.expectOne('/api/auth/first-time-login');
    expect(req.request.method).toBe('POST');
    expect(req.request.url).toBe('/api/auth/first-time-login');
    expect(req.request.url).not.toContain('invite-token-value');
    expect(req.request.body).toEqual({
      token: 'invite-token-value',
      password: 'a long passphrase here',
    });
    req.flush({ status: 'active' });
  });

  it('sends the reset token only inside the JSON body of password-reset/complete', () => {
    service
      .completePasswordReset({ token: 'reset-token-value', password: 'a long passphrase here' })
      .subscribe();
    const req = httpMock.expectOne('/api/auth/password-reset/complete');
    expect(req.request.url).not.toContain('reset-token-value');
    expect(req.request.body).toEqual({
      token: 'reset-token-value',
      password: 'a long passphrase here',
    });
    req.flush({ status: 'password_reset' });
  });
});
