import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';

import { App } from '../app';
import { routes } from '../app.routes';
import { requireVerifiedSession } from '../mobile-auth/auth-data/require-verified-session.guard';
import { MobileCalls } from './mobile-calls/mobile-calls';
import { MobileEvidence } from './mobile-evidence/mobile-evidence';
import { MobileHome } from './mobile-home/mobile-home';
import { MobileSettings } from './mobile-settings/mobile-settings';
import { mobileRoutes } from './mobile.routes';
import { MobileShell } from './mobile-shell/mobile-shell';
import { MobileUnits } from './mobile-units/mobile-units';
import { MobileWelcome } from './mobile-welcome/mobile-welcome';

const ACCOUNT = {
  user_id: 1,
  email: 'a@example.com',
  display_name: 'A',
  role: 'responder',
  status: 'active',
};

// Router navigation (and therefore a guard's HTTP call) is dispatched a few
// microtask turns after navigateByUrl returns its promise, so httpMock
// assertions against a guarded route's request must wait a tick first.
function flushMicrotasks(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

describe('mobile routes', () => {
  it('mounts the mobile children under /mobile', () => {
    expect(routes.some((route) => route.path === 'mobile')).toBe(true);
  });

  it('defines only the approved mobile child routes, each protected content screen guarded', () => {
    const shellRoute = mobileRoutes[0];
    expect(shellRoute.component).toBe(MobileShell);
    expect(shellRoute.canActivate).toBeUndefined();
    expect(shellRoute.canMatch).toBeUndefined();
    expect(shellRoute.children).toEqual([
      { path: '', pathMatch: 'full', redirectTo: 'welcome' },
      { path: 'welcome', component: MobileWelcome },
      { path: 'home', component: MobileHome, canActivate: [requireVerifiedSession] },
      { path: 'calls', component: MobileCalls, canActivate: [requireVerifiedSession] },
      { path: 'units', component: MobileUnits, canActivate: [requireVerifiedSession] },
      { path: 'evidence', component: MobileEvidence, canActivate: [requireVerifiedSession] },
      { path: 'settings', component: MobileSettings, canActivate: [requireVerifiedSession] },
      { path: '**', redirectTo: 'welcome' },
    ]);
    for (const route of shellRoute.children ?? []) {
      expect(route.canMatch).toBeUndefined();
    }
  });

  it('redirects /mobile and unknown mobile paths to the welcome route', async () => {
    TestBed.configureTestingModule({ providers: [provideRouter(routes)] });
    const router = TestBed.inject(Router);

    await router.navigateByUrl('/mobile');
    expect(router.url).toBe('/mobile/welcome');

    await router.navigateByUrl('/mobile/not-a-route');
    expect(router.url).toBe('/mobile/welcome');
  });

  it('reaches the protected mobile content routes once /api/auth/me confirms a session', async () => {
    TestBed.configureTestingModule({
      providers: [provideRouter(routes), provideHttpClient(), provideHttpClientTesting()],
    });
    const router = TestBed.inject(Router);
    const httpMock = TestBed.inject(HttpTestingController);

    for (const path of ['home', 'calls', 'units', 'evidence', 'settings']) {
      const navigation = router.navigateByUrl(`/mobile/${path}`);
      await flushMicrotasks();
      httpMock.expectOne('/api/auth/me').flush(ACCOUNT);
      expect(await navigation).toBe(true);
      expect(router.url).toBe(`/mobile/${path}`);
    }
  });

  it('renders the active mobile navigation semantics and hides navigation on welcome', async () => {
    TestBed.configureTestingModule({
      imports: [App],
      providers: [provideRouter(routes), provideHttpClient(), provideHttpClientTesting()],
    });
    const fixture = TestBed.createComponent(App);
    const router = TestBed.inject(Router);
    const httpMock = TestBed.inject(HttpTestingController);
    fixture.detectChanges();

    const navigation = router.navigateByUrl('/mobile/home');
    await flushMicrotasks();
    httpMock.expectOne('/api/auth/me').flush(ACCOUNT);
    await navigation;
    await fixture.whenStable();
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    const labels = Array.from(
      el.querySelectorAll<HTMLElement>('.mobile-navigation a span:last-child'),
    ).map((label) => label.textContent?.trim());
    expect(labels).toEqual(['Home', 'Calls', 'Units', 'Evidence', 'Settings']);
    const current = el.querySelector('.mobile-navigation [aria-current="page"]');
    expect(current?.textContent?.trim()).toBe('⌂Home');
    expect(el.querySelector('.mobile-main h1')?.textContent).toContain('Home');

    await router.navigateByUrl('/mobile/welcome');
    await fixture.whenStable();
    fixture.detectChanges();
    expect(el.querySelector('.mobile-navigation')).toBeNull();
    expect(el.textContent).toContain('Prototype access — not authentication.');
    fixture.destroy();
  });
});
