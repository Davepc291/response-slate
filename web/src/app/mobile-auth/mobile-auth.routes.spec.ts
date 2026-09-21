import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';

import { App } from '../app';
import { routes } from '../app.routes';
import { BoardPreview } from '../board-preview/board-preview';
import { mobileRoutes } from '../mobile/mobile.routes';
import { requireAdminRole } from './auth-data/require-admin-role.guard';
import { requireVerifiedSession } from './auth-data/require-verified-session.guard';
import { mobileAuthRoutes } from './mobile-auth.routes';

describe('mobile auth routes', () => {
  function configure() {
    TestBed.configureTestingModule({
      imports: [App],
      providers: [provideRouter(routes), provideHttpClient(), provideHttpClientTesting()],
    });
  }

  it('preserves the desktop board and existing mobile children entirely unchanged', () => {
    expect(routes[0]).toEqual({ path: '', component: BoardPreview, pathMatch: 'full' });
    const mobileRoute = routes.find((route) => route.path === 'mobile');
    expect(mobileRoute?.children).toBe(mobileRoutes);
    const authRoute = routes.find((route) => route.path === 'mobile/auth');
    expect(authRoute).toBeDefined();
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
    ]) {
      const result = await router.navigateByUrl(`/mobile/auth/${path}`);
      expect(result).toBe(true);
      expect(router.url).toBe(`/mobile/auth/${path}`);
    }
  });

  it('wires requireVerifiedSession onto the sessions preview route only', () => {
    const shellRoute = mobileAuthRoutes[0];
    const sessionsRoute = shellRoute.children?.find((route) => route.path === 'sessions');
    expect(sessionsRoute?.canActivate).toEqual([requireVerifiedSession]);

    for (const route of shellRoute.children ?? []) {
      if (route.path !== 'sessions') {
        expect(route.canActivate).toBeUndefined();
      }
    }
  });

  it('leaves existing mobile routes reachable and unaffected by the new auth branch', async () => {
    configure();
    const router = TestBed.inject(Router);
    for (const path of ['welcome', 'home', 'calls', 'units', 'evidence', 'settings']) {
      const result = await router.navigateByUrl(`/mobile/${path}`);
      expect(result).toBe(true);
      expect(router.url).toBe(`/mobile/${path}`);
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
