import { TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { of } from 'rxjs';

import { AuthResult, AuthApiService } from '../../mobile-auth/auth-data/auth-api.service';
import { StatusResponse } from '../../mobile-auth/auth-data/auth-api.models';
import { AuthSessionState } from '../../mobile-auth/auth-data/auth-session-state.service';
import { MOBILE_BUILD_INFO } from '../mobile-build-info';
import { MobileSettings } from './mobile-settings';

describe('MobileSettings', () => {
  let navigateByUrl: ReturnType<typeof vi.fn<() => Promise<boolean>>>;
  let logout: ReturnType<typeof vi.fn<() => ReturnType<AuthApiService['logout']>>>;
  let clear: ReturnType<typeof vi.fn>;

  function configure() {
    logout = vi.fn();
    clear = vi.fn();
    TestBed.configureTestingModule({
      imports: [MobileSettings],
      providers: [
        provideRouter([]),
        { provide: AuthApiService, useValue: { logout } },
        { provide: AuthSessionState, useValue: { clear } },
      ],
    });
    // RouterLink in this component's template needs a real, working Router
    // (it reads router configuration during directive construction), so
    // navigation is observed by spying on the real router rather than
    // replacing the Router token with a partial mock — same approach as
    // mobile-auth-sign-in.spec.ts.
    navigateByUrl = vi
      .spyOn(TestBed.inject(Router), 'navigateByUrl')
      .mockResolvedValue(true) as never;
    const fixture = TestBed.createComponent(MobileSettings);
    fixture.detectChanges();
    return fixture;
  }

  it('labels navigator connectivity as a browser hint only', () => {
    const fixture = configure();
    window.dispatchEvent(new Event('offline'));
    fixture.detectChanges();
    const text = (fixture.nativeElement as HTMLElement).textContent ?? '';
    expect(text).toContain('Browser connectivity hint: Offline');
    expect(text).toContain('does not prove CAD, radio, API, or server connectivity or freshness');
    window.dispatchEvent(new Event('online'));
    fixture.destroy();
  });

  it('shows static build information without a runtime endpoint', () => {
    const fixture = configure();
    const text = (fixture.nativeElement as HTMLElement).textContent ?? '';
    expect(text).toContain(MOBILE_BUILD_INFO.applicationVersion);
    expect(text).toContain(MOBILE_BUILD_INFO.buildIdentifier);
    expect(text).toContain(MOBILE_BUILD_INFO.buildDate);
    expect(text).toContain('No version endpoint is contacted');
    fixture.destroy();
  });

  it('does not show the obsolete unauthenticated prototype-exit control', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    // /mobile/settings sits behind requireVerifiedSession, so the old
    // "no authenticated session exists" / "Log out of prototype" copy is
    // factually wrong here and must not reappear — Account → Log out is the
    // one normal sign-out control on this screen.
    expect(el.textContent).not.toContain('Prototype only — no authenticated session exists.');
    expect(el.textContent).not.toContain('Log out of prototype');
    expect(el.textContent).not.toContain('Prototype access');
    fixture.destroy();
  });

  it('shows an Account section with Manage sessions and Log out', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;

    expect(el.textContent).toContain('Account');
    const manageSessionsLink = el.querySelector<HTMLAnchorElement>('a.account-link');
    expect(manageSessionsLink?.textContent?.trim()).toBe('Manage sessions');
    const logoutButton = Array.from(el.querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === 'Log out',
    );
    expect(logoutButton).toBeTruthy();
    // "Log out everywhere" is a destructive action that stays on
    // /mobile/auth/sessions only, never on this screen.
    expect(el.textContent).not.toContain('Log out everywhere');
    fixture.destroy();
  });

  it('routes Manage sessions to the existing /mobile/auth/sessions page', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    const manageSessionsLink = el.querySelector<HTMLAnchorElement>('a.account-link');
    expect(manageSessionsLink?.getAttribute('href')).toBe('/mobile/auth/sessions');
    fixture.destroy();
  });

  it('logs out through the existing AuthApiService.logout() flow and returns to sign-in', () => {
    const fixture = configure();
    logout.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'logged_out' } }),
    );
    const el = fixture.nativeElement as HTMLElement;
    const logoutButton = Array.from(el.querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === 'Log out',
    );
    logoutButton?.dispatchEvent(new Event('click'));

    expect(logout).toHaveBeenCalledOnce();
    expect(clear).toHaveBeenCalledOnce();
    expect(navigateByUrl).toHaveBeenCalledExactlyOnceWith('/mobile/auth/sign-in');
    fixture.destroy();
  });

  it('does not clear local state or navigate away when the logout call itself fails', () => {
    const fixture = configure();
    logout.mockReturnValue(
      of<AuthResult<StatusResponse>>({
        ok: false,
        error: { kind: 'unavailable', message: 'Sign-in is temporarily unavailable.' },
      }),
    );
    const el = fixture.nativeElement as HTMLElement;
    const logoutButton = Array.from(el.querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === 'Log out',
    );
    logoutButton?.dispatchEvent(new Event('click'));
    fixture.detectChanges();

    // A failed server-side logout must never present as a successful
    // sign-out: clearing local state or navigating away here would falsely
    // tell the user they are signed out while the session may still be
    // fully valid.
    expect(clear).not.toHaveBeenCalled();
    expect(navigateByUrl).not.toHaveBeenCalled();
    expect(el.textContent).toContain('Unable to log out right now.');
    fixture.destroy();
  });
});
