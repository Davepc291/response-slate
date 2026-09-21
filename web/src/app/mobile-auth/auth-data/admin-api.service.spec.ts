import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';

import { AdminApiService, AdminResult } from './admin-api.service';
import { AdminCreateUserResponse, AdminUserView } from './admin-api.models';

describe('AdminApiService', () => {
  let service: AdminApiService;
  let httpMock: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    service = TestBed.inject(AdminApiService);
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    httpMock.verify();
  });

  it('lists users against a same-origin relative URL with credentials included and bounded query params', () => {
    service.listUsers({ search: 'a b', role: 'responder', limit: 10, offset: 20 }).subscribe();
    const req = httpMock.expectOne('/api/admin/users?role=responder&search=a+b&limit=10&offset=20');
    expect(req.request.method).toBe('GET');
    expect(req.request.withCredentials).toBe(true);
    req.flush({ users: [], limit: 10, offset: 20 });
  });

  it('lists users with no query string when no params are given', () => {
    service.listUsers().subscribe();
    const req = httpMock.expectOne('/api/admin/users');
    expect(req.request.method).toBe('GET');
    req.flush({ users: [], limit: 25, offset: 0 });
  });

  it('gets one user by numeric id', () => {
    service.getUser(42).subscribe();
    const req = httpMock.expectOne('/api/admin/users/42');
    expect(req.request.method).toBe('GET');
    req.flush({
      id: 42,
      email: 'a@example.test',
      display_name: 'A',
      role: 'responder',
      status: 'active',
      created_at: '2026-01-01T00:00:00Z',
    } satisfies AdminUserView);
  });

  it('creates a user and surfaces the one-time invitation code in the response value only', () => {
    let captured: AdminResult<AdminCreateUserResponse> | undefined;
    service
      .createUser({
        email: 'new@example.test',
        display_name: 'New',
        role: 'responder',
        scope: 'engine-1',
      })
      .subscribe((r) => {
        captured = r;
      });

    const req = httpMock.expectOne('/api/admin/users');
    expect(req.request.method).toBe('POST');
    expect(req.request.withCredentials).toBe(true);
    expect(req.request.body).toEqual({
      email: 'new@example.test',
      display_name: 'New',
      role: 'responder',
      scope: 'engine-1',
    });

    const response: AdminCreateUserResponse = {
      user: {
        id: 7,
        email: 'new@example.test',
        display_name: 'New',
        role: 'responder',
        scope: 'engine-1',
        status: 'invited',
        created_at: '2026-01-01T00:00:00Z',
      },
      invitation: {
        code: 'raw-one-time-code',
        expires_at: '2026-01-02T00:00:00Z',
        sensitive: true,
        warning: 'Sensitive — shown once.',
      },
    };
    req.flush(response, { status: 201, statusText: 'Created' });

    expect(captured).toEqual({ ok: true, value: response });
  });

  it('maps a forbidden error envelope to the safe UI failure', () => {
    let captured: AdminResult<AdminUserView> | undefined;
    service.getUser(1).subscribe((r) => {
      captured = r;
    });
    httpMock
      .expectOne('/api/admin/users/1')
      .flush(
        { error: { code: 'forbidden', message: 'internal detail never shown' } },
        { status: 403, statusText: 'Forbidden' },
      );

    expect(captured).toEqual({
      ok: false,
      error: { kind: 'forbidden', message: 'You are not authorized to perform this action.' },
    });
  });

  it('maps a self_action_forbidden error envelope distinctly', () => {
    let captured: AdminResult<{ status: string }> | undefined;
    service.suspend(1).subscribe((r) => {
      captured = r;
    });
    httpMock
      .expectOne('/api/admin/users/1/suspend')
      .flush(
        { error: { code: 'self_action_forbidden', message: 'x' } },
        { status: 403, statusText: 'Forbidden' },
      );

    expect(captured).toEqual({
      ok: false,
      error: {
        kind: 'self_action_forbidden',
        message: 'You cannot perform this action on your own account.',
      },
    });
  });

  it('never surfaces a raw backend message for an unrecognized error code', () => {
    let captured: AdminResult<AdminUserView> | undefined;
    service.getUser(1).subscribe((r) => {
      captured = r;
    });
    httpMock
      .expectOne('/api/admin/users/1')
      .flush(
        { error: { code: 'some_internal_failure', message: 'pq: connection refused at 10.0.0.5' } },
        { status: 500, statusText: 'Internal Server Error' },
      );

    expect(captured?.ok).toBe(false);
    if (captured && !captured.ok) {
      expect(captured.error.kind).toBe('unavailable');
      expect(captured.error.message).not.toContain('pq:');
      expect(captured.error.message).not.toContain('10.0.0.5');
    }
  });

  it('resends an invitation and resets a credential against their exact action routes', () => {
    service.resendInvitation(5).subscribe();
    httpMock
      .expectOne('/api/admin/users/5/resend-invitation')
      .flush({ invitation: { code: 'c1', expires_at: 'e1', sensitive: true, warning: 'w' } });

    service.resetCredential(5).subscribe();
    httpMock
      .expectOne('/api/admin/users/5/reset-credential')
      .flush({ invitation: { code: 'c2', expires_at: 'e2', sensitive: true, warning: 'w' } });
  });

  it('changes role/scope with the request body only, never in the URL', () => {
    service.changeRole(9, { role: 'dispatcher_operator', scope: 'engine-2' }).subscribe();
    const req = httpMock.expectOne('/api/admin/users/9/role');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({ role: 'dispatcher_operator', scope: 'engine-2' });
    req.flush({ status: 'updated' });
  });

  it('revokes sessions and disables/restores against their exact action routes', () => {
    service.revokeSessions(3).subscribe();
    httpMock.expectOne('/api/admin/users/3/revoke-sessions').flush({ revoked_count: 2 });

    service.disable(3).subscribe();
    httpMock.expectOne('/api/admin/users/3/disable').flush({ status: 'disabled' });

    service.restore(3).subscribe();
    httpMock.expectOne('/api/admin/users/3/restore').flush({ status: 'active' });
  });

  it('maps a network-level failure (status 0) to the generic unavailable state, never queued for retry', () => {
    let captured: AdminResult<AdminUserView> | undefined;
    service.getUser(1).subscribe((r) => {
      captured = r;
    });
    httpMock.expectOne('/api/admin/users/1').error(new ProgressEvent('error'), { status: 0 });
    expect(captured).toEqual({
      ok: false,
      error: { kind: 'unavailable', message: 'This action is temporarily unavailable.' },
    });
  });
});
