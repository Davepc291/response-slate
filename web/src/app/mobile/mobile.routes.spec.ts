import { TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';

import { App } from '../app';
import { routes } from '../app.routes';
import { BoardPreview } from '../board-preview/board-preview';
import { MobileCalls } from './mobile-calls/mobile-calls';
import { MobileEvidence } from './mobile-evidence/mobile-evidence';
import { MobileHome } from './mobile-home/mobile-home';
import { MobileSettings } from './mobile-settings/mobile-settings';
import { mobileRoutes } from './mobile.routes';
import { MobileShell } from './mobile-shell/mobile-shell';
import { MobileUnits } from './mobile-units/mobile-units';
import { MobileWelcome } from './mobile-welcome/mobile-welcome';

describe('mobile routes', () => {
  it('preserves the desktop board as the exact root route', () => {
    expect(routes[0]).toEqual({ path: '', component: BoardPreview, pathMatch: 'full' });
    expect(routes[1]?.path).toBe('mobile');
  });

  it('loads the existing BoardPreview at /', async () => {
    TestBed.configureTestingModule({ imports: [App], providers: [provideRouter(routes)] });
    const fixture = TestBed.createComponent(App);
    const router = TestBed.inject(Router);

    await router.navigateByUrl('/');
    await fixture.whenStable();
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('app-board-preview .shadow-board')).not.toBeNull();
    fixture.destroy();
  });

  it('defines only the approved open mobile child routes', () => {
    const shellRoute = mobileRoutes[0];
    expect(shellRoute.component).toBe(MobileShell);
    expect(shellRoute.canActivate).toBeUndefined();
    expect(shellRoute.canMatch).toBeUndefined();
    expect(shellRoute.children).toEqual([
      { path: '', pathMatch: 'full', redirectTo: 'welcome' },
      { path: 'welcome', component: MobileWelcome },
      { path: 'home', component: MobileHome },
      { path: 'calls', component: MobileCalls },
      { path: 'units', component: MobileUnits },
      { path: 'evidence', component: MobileEvidence },
      { path: 'settings', component: MobileSettings },
      { path: '**', redirectTo: 'welcome' },
    ]);
    for (const route of shellRoute.children ?? []) {
      expect(route.canActivate).toBeUndefined();
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

  it('keeps direct mobile routes intentionally accessible', async () => {
    TestBed.configureTestingModule({ providers: [provideRouter(routes)] });
    const router = TestBed.inject(Router);

    for (const path of ['home', 'calls', 'units', 'evidence', 'settings']) {
      const result = await router.navigateByUrl(`/mobile/${path}`);
      expect(result).toBe(true);
      expect(router.url).toBe(`/mobile/${path}`);
    }
  });

  it('renders the active mobile navigation semantics and hides navigation on welcome', async () => {
    TestBed.configureTestingModule({ imports: [App], providers: [provideRouter(routes)] });
    const fixture = TestBed.createComponent(App);
    const router = TestBed.inject(Router);
    fixture.detectChanges();

    await router.navigateByUrl('/mobile/home');
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
