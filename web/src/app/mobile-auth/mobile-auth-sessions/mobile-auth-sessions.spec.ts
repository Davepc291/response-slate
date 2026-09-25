import { TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { of } from 'rxjs';

import { AuthResult } from '../auth-data/auth-api.service';
import { AuthApiService } from '../auth-data/auth-api.service';
import { SessionView, StatusResponse } from '../auth-data/auth-api.models';
import { AuthSessionState } from '../auth-data/auth-session-state.service';
import { MobileAuthSessions } from './mobile-auth-sessions';

const SESSIONS: SessionView[] = [
  {
    id: 1,
    device_hint: 'This browser',
    created_at: '2026-01-01T00:00:00Z',
    last_seen_at: '2026-01-02T00:00:00Z',
    expires_at: '2026-02-01T00:00:00Z',
    current: true,
  },
  {
    id: 2,
    device_hint: 'Another device',
    created_at: '2026-01-01T00:00:00Z',
    last_seen_at: '2026-01-01T12:00:00Z',
    expires_at: '2026-02-01T00:00:00Z',
    current: false,
  },
];

describe('MobileAuthSessions', () => {
  let sessions: ReturnType<typeof vi.fn<() => ReturnType<AuthApiService['sessions']>>>;
  let revokeSession: ReturnType<typeof vi.fn<() => ReturnType<AuthApiService['revokeSession']>>>;
  let logout: ReturnType<typeof vi.fn<() => ReturnType<AuthApiService['logout']>>>;
  let logoutAll: ReturnType<typeof vi.fn<() => ReturnType<AuthApiService['logoutAll']>>>;
  let navigateByUrl: ReturnType<typeof vi.fn<() => Promise<boolean>>>;
  let clear: ReturnType<typeof vi.fn>;

  function configure() {
    sessions = vi
      .fn()
      .mockReturnValue(of<AuthResult<SessionView[]>>({ ok: true, value: SESSIONS }));
    revokeSession = vi.fn();
    logout = vi.fn();
    logoutAll = vi.fn();
    navigateByUrl = vi.fn().mockResolvedValue(true);
    clear = vi.fn();

    TestBed.configureTestingModule({
      imports: [MobileAuthSessions],
      providers: [
        provideRouter([]),
        { provide: AuthApiService, useValue: { sessions, revokeSession, logout, logoutAll } },
        {
          provide: AuthSessionState,
          useValue: { account: () => null, refresh: vi.fn(), clear },
        },
        { provide: Router, useValue: { navigateByUrl } },
      ],
    });
    const fixture = TestBed.createComponent(MobileAuthSessions);
    fixture.detectChanges();
    return fixture;
  }

  it('lists sessions and marks the current device, without showing cookie/token/IP detail', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('This browser');
    expect(el.textContent).toContain('Another device');
    expect(el.textContent).toContain('This device');
    expect(el.innerHTML).not.toMatch(/__Host-gfr_session|token|\b\d{1,3}(\.\d{1,3}){3}\b/i);
    fixture.destroy();
  });

  it('only offers a revoke control for non-current sessions', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    const items = Array.from(el.querySelectorAll('.session-item'));
    expect(items).toHaveLength(2);
    expect(items[0].querySelector('button')).toBeNull();
    expect(items[1].querySelector('button')?.textContent?.trim()).toBe('Revoke');
    fixture.destroy();
  });

  it('revokes the selected owned session and reloads the list', () => {
    const fixture = configure();
    revokeSession.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'revoked' } }),
    );
    const el = fixture.nativeElement as HTMLElement;
    el.querySelectorAll('.session-item')[1]
      .querySelector('button')
      ?.dispatchEvent(new Event('click'));
    fixture.detectChanges();

    expect(revokeSession).toHaveBeenCalledExactlyOnceWith(2);
    expect(sessions).toHaveBeenCalledTimes(2);
    fixture.destroy();
  });

  it('shows a generic failure message when revocation fails', () => {
    const fixture = configure();
    revokeSession.mockReturnValue(
      of<AuthResult<StatusResponse>>({
        ok: false,
        error: { kind: 'unavailable', message: 'Sign-in is temporarily unavailable.' },
      }),
    );
    const el = fixture.nativeElement as HTMLElement;
    el.querySelectorAll('.session-item')[1]
      .querySelector('button')
      ?.dispatchEvent(new Event('click'));
    fixture.detectChanges();

    expect(el.textContent).toContain('Unable to revoke that session right now.');
    fixture.destroy();
  });

  it('logs out the current session and returns to sign-in', () => {
    const fixture = configure();
    logout.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'logged_out' } }),
    );
    const el = fixture.nativeElement as HTMLElement;
    const signOutButton = Array.from(el.querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === 'Log out',
    );
    signOutButton?.dispatchEvent(new Event('click'));

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
    const signOutButton = Array.from(el.querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === 'Log out',
    );
    signOutButton?.dispatchEvent(new Event('click'));
    fixture.detectChanges();

    // A failed server-side logout must never present as a successful
    // sign-out: the session may still be fully valid and authorized, so
    // clearing local state or navigating to sign-in here would falsely
    // tell the user they are signed out.
    expect(clear).not.toHaveBeenCalled();
    expect(navigateByUrl).not.toHaveBeenCalled();
    expect(el.textContent).toContain('Unable to log out right now.');
    fixture.destroy();
  });

  it('does not clear local state or navigate away when the logout-everywhere call itself fails', () => {
    const fixture = configure();
    logoutAll.mockReturnValue(
      of<AuthResult<StatusResponse>>({
        ok: false,
        error: { kind: 'unavailable', message: 'Sign-in is temporarily unavailable.' },
      }),
    );
    const el = fixture.nativeElement as HTMLElement;
    const signOutAllButton = Array.from(el.querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === 'Log out everywhere',
    );
    signOutAllButton?.dispatchEvent(new Event('click'));
    fixture.detectChanges();

    expect(clear).not.toHaveBeenCalled();
    expect(navigateByUrl).not.toHaveBeenCalled();
    expect(el.textContent).toContain('Unable to log out right now.');
    fixture.destroy();
  });

  it('logs out everywhere and returns to sign-in', () => {
    const fixture = configure();
    logoutAll.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'logged_out_everywhere' } }),
    );
    const el = fixture.nativeElement as HTMLElement;
    const signOutAllButton = Array.from(el.querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === 'Log out everywhere',
    );
    signOutAllButton?.dispatchEvent(new Event('click'));

    expect(logoutAll).toHaveBeenCalledOnce();
    expect(clear).toHaveBeenCalledOnce();
    expect(navigateByUrl).toHaveBeenCalledExactlyOnceWith('/mobile/auth/sign-in');
    fixture.destroy();
  });
});
