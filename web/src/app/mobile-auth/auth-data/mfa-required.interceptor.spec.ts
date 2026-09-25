import { HttpClient, provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';

import { mfaRequiredInterceptor } from './mfa-required.interceptor';

describe('mfaRequiredInterceptor', () => {
  let http: HttpClient;
  let httpMock: HttpTestingController;
  let navigateByUrl: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [
        provideRouter([]),
        provideHttpClient(withInterceptors([mfaRequiredInterceptor])),
        provideHttpClientTesting(),
      ],
    });
    http = TestBed.inject(HttpClient);
    httpMock = TestBed.inject(HttpTestingController);
    navigateByUrl = vi
      .spyOn(TestBed.inject(Router), 'navigateByUrl')
      .mockResolvedValue(true) as never;
  });

  afterEach(() => {
    httpMock.verify();
  });

  it('navigates to MFA verification on a 403 mfa_verification_required from an /api/admin/* route', () => {
    http.get('/api/admin/users').subscribe({ error: () => {} });
    httpMock
      .expectOne('/api/admin/users')
      .flush(
        { error: { code: 'mfa_verification_required', message: 'internal' } },
        { status: 403, statusText: 'Forbidden' },
      );

    expect(navigateByUrl).toHaveBeenCalledExactlyOnceWith(
      '/mobile/auth/mfa/verify?returnUrl=%2F',
    );
  });

  it('still propagates the original error to the caller (convenience only, not a swallow)', () => {
    let captured: unknown;
    http.get('/api/admin/users').subscribe({ error: (e) => (captured = e) });
    httpMock
      .expectOne('/api/admin/users')
      .flush(
        { error: { code: 'mfa_verification_required', message: 'internal' } },
        { status: 403, statusText: 'Forbidden' },
      );

    expect(captured).toBeDefined();
  });

  it('does not navigate on a 403 with a different error code', () => {
    http.get('/api/admin/users').subscribe({ error: () => {} });
    httpMock
      .expectOne('/api/admin/users')
      .flush(
        { error: { code: 'forbidden', message: 'internal' } },
        { status: 403, statusText: 'Forbidden' },
      );

    expect(navigateByUrl).not.toHaveBeenCalled();
  });

  it('does not navigate on a 403 mfa_verification_required from a non-admin route', () => {
    http.post('/api/auth/mfa/verify', { action: 'finish' }).subscribe({ error: () => {} });
    httpMock
      .expectOne('/api/auth/mfa/verify')
      .flush(
        { error: { code: 'mfa_verification_required', message: 'internal' } },
        { status: 403, statusText: 'Forbidden' },
      );

    expect(navigateByUrl).not.toHaveBeenCalled();
  });

  it('does not navigate on a non-403 error from an admin route', () => {
    http.get('/api/admin/users').subscribe({ error: () => {} });
    httpMock.expectOne('/api/admin/users').flush(null, { status: 503, statusText: 'Unavailable' });

    expect(navigateByUrl).not.toHaveBeenCalled();
  });
});
