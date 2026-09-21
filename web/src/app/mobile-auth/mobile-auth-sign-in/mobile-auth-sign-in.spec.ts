import { TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { Subject, of } from 'rxjs';

import { AuthResult } from '../auth-data/auth-api.service';
import { AuthApiService } from '../auth-data/auth-api.service';
import { MeResponse } from '../auth-data/auth-api.models';
import { AuthSessionState } from '../auth-data/auth-session-state.service';
import { MobileAuthSignIn } from './mobile-auth-sign-in';

const ACCOUNT: MeResponse = {
  user_id: 1,
  email: 'a@example.com',
  display_name: 'A',
  role: 'responder',
  status: 'active',
};

describe('MobileAuthSignIn', () => {
  let login: ReturnType<typeof vi.fn<() => ReturnType<AuthApiService['login']>>>;
  let me: ReturnType<typeof vi.fn<() => ReturnType<AuthApiService['me']>>>;
  let navigateByUrl: ReturnType<typeof vi.fn<() => Promise<boolean>>>;
  let setAuthenticated: ReturnType<typeof vi.fn>;

  function configure() {
    login = vi.fn();
    me = vi.fn();
    setAuthenticated = vi.fn();

    TestBed.configureTestingModule({
      imports: [MobileAuthSignIn],
      providers: [
        provideRouter([]),
        { provide: AuthApiService, useValue: { login, me } },
        { provide: AuthSessionState, useValue: { setAuthenticated } },
      ],
    });
    // RouterLink in this component's template needs a real, working Router
    // (it reads router configuration during directive construction), so
    // navigation is observed by spying on the real router rather than
    // replacing the Router token with a partial mock.
    navigateByUrl = vi
      .spyOn(TestBed.inject(Router), 'navigateByUrl')
      .mockResolvedValue(true) as never;
    const fixture = TestBed.createComponent(MobileAuthSignIn);
    fixture.detectChanges();
    return fixture;
  }

  function fillAndSubmit(fixture: ReturnType<typeof configure>, email: string, password: string) {
    const el = fixture.nativeElement as HTMLElement;
    const emailInput = el.querySelector<HTMLInputElement>('#auth-sign-in-email')!;
    const passwordInput = el.querySelector<HTMLInputElement>('#auth-sign-in-password')!;
    emailInput.value = email;
    emailInput.dispatchEvent(new Event('input', { bubbles: true }));
    passwordInput.value = password;
    passwordInput.dispatchEvent(new Event('input', { bubbles: true }));
    fixture.detectChanges();
    el.querySelector('form')?.dispatchEvent(
      new Event('submit', { bubbles: true, cancelable: true }),
    );
    fixture.detectChanges();
  }

  it('shows no sign-up/create-account link and no synthetic labeling', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).not.toMatch(/sign up|create account|register/i);
    expect(el.textContent).toContain('Authorized users only');
    fixture.destroy();
  });

  it('renders a real form with the required password-manager autocomplete hints', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('form')).not.toBeNull();
    expect(el.querySelector('#auth-sign-in-email')?.getAttribute('autocomplete')).toBe('username');
    expect(el.querySelector('#auth-sign-in-password')?.getAttribute('autocomplete')).toBe(
      'current-password',
    );
    fixture.destroy();
  });

  it('toggles password visibility with an accessible name and field type', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    const passwordInput = el.querySelector<HTMLInputElement>('#auth-sign-in-password')!;
    const toggle = el.querySelector<HTMLButtonElement>('.visibility-toggle')!;

    expect(passwordInput.type).toBe('password');
    expect(toggle.textContent?.trim()).toBe('Show password');

    toggle.click();
    fixture.detectChanges();
    expect(passwordInput.type).toBe('text');
    expect(toggle.textContent?.trim()).toBe('Hide password');

    fixture.destroy();
  });

  it('submits only email/password to POST /api/auth/login, never a fabricated success', () => {
    const fixture = configure();
    login.mockReturnValue(
      of<AuthResult<MeResponse>>({
        ok: false,
        error: { kind: 'invalid_credentials', message: 'That email or password is incorrect.' },
      }),
    );

    fillAndSubmit(fixture, 'a@example.com', 'wrongpassword');

    expect(login).toHaveBeenCalledExactlyOnceWith({
      email: 'a@example.com',
      password: 'wrongpassword',
    });
    expect(navigateByUrl).not.toHaveBeenCalled();
    fixture.destroy();
  });

  it('shows the exact generic invalid-credentials message without revealing account existence', () => {
    const fixture = configure();
    login.mockReturnValue(
      of<AuthResult<MeResponse>>({
        ok: false,
        error: { kind: 'invalid_credentials', message: 'That email or password is incorrect.' },
      }),
    );
    fillAndSubmit(fixture, 'unknown@example.com', 'anything');
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('That email or password is incorrect.');
    fixture.destroy();
  });

  it('disables the submit button and prevents a duplicate submission while a request is in flight', () => {
    const fixture = configure();
    const pending = new Subject<AuthResult<MeResponse>>();
    login.mockReturnValue(pending.asObservable());

    fillAndSubmit(fixture, 'a@example.com', 'a-password');
    const el = fixture.nativeElement as HTMLElement;
    const submitButton = el.querySelector<HTMLButtonElement>('button.primary')!;
    expect(submitButton.disabled).toBe(true);
    expect(submitButton.textContent?.trim()).toBe('Signing in…');

    el.querySelector('form')?.dispatchEvent(
      new Event('submit', { bubbles: true, cancelable: true }),
    );
    expect(login).toHaveBeenCalledOnce();

    pending.next({
      ok: false,
      error: { kind: 'invalid_credentials', message: 'That email or password is incorrect.' },
    });
    fixture.destroy();
  });

  it('clears the password field only after the response is handled', () => {
    const fixture = configure();
    const pending = new Subject<AuthResult<MeResponse>>();
    login.mockReturnValue(pending.asObservable());
    fillAndSubmit(fixture, 'a@example.com', 'a-password');

    const passwordInput = (fixture.nativeElement as HTMLElement).querySelector<HTMLInputElement>(
      '#auth-sign-in-password',
    )!;
    expect(passwordInput.value).toBe('a-password');

    pending.next({
      ok: false,
      error: { kind: 'invalid_credentials', message: 'That email or password is incorrect.' },
    });
    fixture.detectChanges();
    expect(passwordInput.value).toBe('');
    fixture.destroy();
  });

  it('verifies /api/auth/me before treating the session as established, then navigates to the preview sessions screen', () => {
    const fixture = configure();
    login.mockReturnValue(of<AuthResult<MeResponse>>({ ok: true, value: ACCOUNT }));
    me.mockReturnValue(of<AuthResult<MeResponse>>({ ok: true, value: ACCOUNT }));

    fillAndSubmit(fixture, 'a@example.com', 'correct-password');

    expect(me).toHaveBeenCalledOnce();
    expect(setAuthenticated).toHaveBeenCalledExactlyOnceWith(ACCOUNT);
    expect(navigateByUrl).toHaveBeenCalledExactlyOnceWith('/mobile/auth/sessions');
    fixture.destroy();
  });

  it('navigates to the explicit service-unavailable preview route without exposing technical detail', () => {
    const fixture = configure();
    login.mockReturnValue(
      of<AuthResult<MeResponse>>({
        ok: false,
        error: { kind: 'unavailable', message: 'Sign-in is temporarily unavailable.' },
      }),
    );
    fillAndSubmit(fixture, 'a@example.com', 'a-password');
    expect(navigateByUrl).toHaveBeenCalledExactlyOnceWith('/mobile/auth/service-unavailable');
    fixture.destroy();
  });

  it('writes no browser storage during a full submit cycle', () => {
    const fixture = configure();
    login.mockReturnValue(of<AuthResult<MeResponse>>({ ok: true, value: ACCOUNT }));
    me.mockReturnValue(of<AuthResult<MeResponse>>({ ok: true, value: ACCOUNT }));
    const storageWrite = vi.spyOn(Storage.prototype, 'setItem');

    fillAndSubmit(fixture, 'a@example.com', 'a-password');

    expect(storageWrite).not.toHaveBeenCalled();
    storageWrite.mockRestore();
    fixture.destroy();
  });
});
