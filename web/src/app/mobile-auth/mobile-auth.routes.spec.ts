import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';

import { App } from '../app';
import { routes } from '../app.routes';
import { BoardPreview } from '../board-preview/board-preview';
import { mobileRoutes } from '../mobile/mobile.routes';
import { requireAdminRole } from './auth-data/require-admin-role.guard';
import { redirectRoot } from './auth-data/redirect-root.guard';
import { requireVerifiedSession } from './auth-data/require-verified-session.guard';
import { mobileAuthRoutes } from './mobile-auth.routes';

// Router navigation (and therefore a guard's HTTP call) is dispatched a few
// microtask turns after navigateByUrl returns its promise, so httpMock
// assertions against a guarded route's request must wait a tick first.
function flushMicrotasks(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

describe('mobile auth routes', () => {
  function configure() {
    TestBed.configureTestingModule({
      imports: [App],
      providers: [provideRouter(routes), provideHttpClient(), provideHttpClientTesting()],
    });
  }

  it('gates the root route behind redirectRoot and preserves the mobile/auth and mobile children', () => {
    expect(routes[0]).toEqual({
      path: '',
      pathMatch: 'full',
      canActivate: [redirectRoot],
      children: [],
    });
    const previewRoute = routes.find((route) => route.path === 'preview');
    expect(previewRoute).toEqual({ path: 'preview', component: BoardPreview });
    const mobileRoute = routes.find((route) => route.path === 'mobile');
    expect(mobileRoute?.children).toBe(mobileRoutes);
    const authRoute = routes.find((route) => route.path === 'mobile/auth');
    expect(authRoute).toBeDefined();
  });

  it('sends an unauthenticated visitor at / to sign-in', async () => {
    configure();
    const router = TestBed.inject(Router);
    const httpMock = TestBed.inject(HttpTestingController);

    const navigation = router.navigateByUrl('/');
    await flushMicrotasks();
    httpMock
      .expectOne('/api/auth/me')
      .flush(
        { error: { code: 'not_authenticated', message: 'Sign-in required.' } },
        { status: 401, statusText: 'Unauthorized' },
      );
    await navigation;

    expect(router.url).toBe('/mobile/auth/sign-in');
  });

  it('sends an authenticated visitor at / into the protected mobile area', async () => {
    configure();
    const router = TestBed.inject(Router);
    const httpMock = TestBed.inject(HttpTestingController);
    const account = {
      user_id: 1,
      email: 'a@example.com',
      display_name: 'A',
      role: 'responder',
      status: 'active',
    };

    // redirectRoot's own /api/auth/me check redirects to /mobile/home, whose
    // requireVerifiedSession guard then performs its own, separate check —
    // two requests for one navigateByUrl('/') call.
    const navigation = router.navigateByUrl('/');
    await flushMicrotasks();
    httpMock.expectOne('/api/auth/me').flush(account);
    await flushMicrotasks();
    httpMock.expectOne('/api/auth/me').flush(account);
    expect(await navigation).toBe(true);

    expect(router.url).toBe('/mobile/home');
  });

  it('still reaches the synthetic BoardPreview board at the explicit /preview route', async () => {
    configure();
    const fixture = TestBed.createComponent(App);
    const router = TestBed.inject(Router);

    const result = await router.navigateByUrl('/preview');
    await fixture.whenStable();
    fixture.detectChanges();

    expect(result).toBe(true);
    expect(router.url).toBe('/preview');
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('app-board-preview .shadow-board')).not.toBeNull();
    fixture.destroy();
  });

  it('loads the sign-in screen at /mobile/auth/sign-in', async () => {
    configure();
    const fixture = TestBed.createComponent(App);
    const router = TestBed.inject(Router);

    const result = await router.navigateByUrl('/mobile/auth/sign-in');
    await fixture.whenStable();
    fixture.detectChanges();

    expect(result).toBe(true);
    expect(router.url).toBe('/mobile/auth/sign-in');
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('h1')?.textContent).toContain('Sign in');
    fixture.destroy();
  });

  it('redirects /mobile/auth (no trailing segment) to sign-in', async () => {
    configure();
    const router = TestBed.inject(Router);
    await router.navigateByUrl('/mobile/auth');
    expect(router.url).toBe('/mobile/auth/sign-in');
  });

  it('redirects an unknown /mobile/auth/** path to sign-in', async () => {
    configure();
    const router = TestBed.inject(Router);
    await router.navigateByUrl('/mobile/auth/not-a-real-screen');
    expect(router.url).toBe('/mobile/auth/sign-in');
  });

  it('reaches every explicit preview route directly', async () => {
    configure();
    const router = TestBed.inject(Router);
    for (const path of [
      'first-time-access',
      'forgot-password',
      'reset-password',
      'service-unavailable',
      'mfa/enroll',
    ]) {
      const result = await router.navigateByUrl(`/mobile/auth/${path}`);
      expect(result).toBe(true);
      expect(router.url).toBe(`/mobile/auth/${path}`);
    }
  });

  it('wires requireVerifiedSession onto the sessions and MFA verification preview routes only', () => {
    const shellRoute = mobileAuthRoutes[0];
    const sessionsRoute = shellRoute.children?.find((route) => route.path === 'sessions');
    const verifyRoute = shellRoute.children?.find((route) => route.path === 'mfa/verify');
    expect(sessionsRoute?.canActivate).toEqual([requireVerifiedSession]);
    expect(verifyRoute?.canActivate).toEqual([requireVerifiedSession]);

    for (const route of shellRoute.children ?? []) {
      if (route.path !== 'sessions' && route.path !== 'mfa/verify' && route.path !== 'admin') {
        expect(route.canActivate).toBeUndefined();
      }
    }
  });

  it('leaves the MFA enrollment route unguarded, unlike verification', () => {
    const shellRoute = mobileAuthRoutes[0];
    const enrollRoute = shellRoute.children?.find((route) => route.path === 'mfa/enroll');
    expect(enrollRoute).toBeDefined();
    expect(enrollRoute?.canActivate).toBeUndefined();
  });

  it('leaves the unguarded welcome route reachable without a session', async () => {
    configure();
    const router = TestBed.inject(Router);
    const result = await router.navigateByUrl('/mobile/welcome');
    expect(result).toBe(true);
    expect(router.url).toBe('/mobile/welcome');
  });

  it('reaches every protected mobile content route once /api/auth/me confirms a session', async () => {
    configure();
    const router = TestBed.inject(Router);
    const httpMock = TestBed.inject(HttpTestingController);
    for (const path of ['home', 'calls', 'units', 'evidence', 'settings']) {
      const navigation = router.navigateByUrl(`/mobile/${path}`);
      await flushMicrotasks();
      httpMock.expectOne('/api/auth/me').flush({
        user_id: 1,
        email: 'a@example.com',
        display_name: 'A',
        role: 'responder',
        status: 'active',
      });
      expect(await navigation).toBe(true);
      expect(router.url).toBe(`/mobile/${path}`);
    }
  });

  it('redirects an unauthenticated visitor away from every protected mobile content route', async () => {
    configure();
    const router = TestBed.inject(Router);
    const httpMock = TestBed.inject(HttpTestingController);
    for (const path of ['home', 'calls', 'units', 'evidence', 'settings']) {
      const navigation = router.navigateByUrl(`/mobile/${path}`);
      await flushMicrotasks();
      httpMock
        .expectOne('/api/auth/me')
        .flush(
          { error: { code: 'not_authenticated', message: 'Sign-in required.' } },
          { status: 401, statusText: 'Unauthorized' },
        );
      await navigation;
      expect(router.url).toBe('/mobile/auth/sign-in');
    }
  });

  it('wires requireVerifiedSession and requireAdminRole onto every admin preview route except access-denied', () => {
    const shellRoute = mobileAuthRoutes[0];
    const adminRoute = shellRoute.children?.find((route) => route.path === 'admin');
    expect(adminRoute).toBeDefined();
    expect(adminRoute?.canActivate).toBeUndefined();

    const guarded = ['users', 'users/new', 'users/:id'];
    for (const path of guarded) {
      const route = adminRoute?.children?.find((r) => r.path === path);
      expect(route?.canActivate).toEqual([requireVerifiedSession, requireAdminRole]);
    }

    const accessDenied = adminRoute?.children?.find((r) => r.path === 'access-denied');
    expect(accessDenied?.canActivate).toBeUndefined();
  });

  it('redirects an unknown /mobile/auth/admin/** path to the user list, not sign-in', async () => {
    const adminRoute = mobileAuthRoutes[0].children?.find((route) => route.path === 'admin');
    const wildcard = adminRoute?.children?.find((route) => route.path === '**');
    expect(wildcard?.redirectTo).toBe('users');
  });

  it('reaches the unguarded access-denied screen directly', async () => {
    configure();
    const fixture = TestBed.createComponent(App);
    const router = TestBed.inject(Router);

    const result = await router.navigateByUrl('/mobile/auth/admin/access-denied');
    await fixture.whenStable();
    fixture.detectChanges();

    expect(result).toBe(true);
    expect(router.url).toBe('/mobile/auth/admin/access-denied');
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('h1')?.textContent).toContain('Access denied');
    fixture.destroy();
  });
});
