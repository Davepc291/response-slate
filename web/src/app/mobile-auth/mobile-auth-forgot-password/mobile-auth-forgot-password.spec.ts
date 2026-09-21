import { TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { of } from 'rxjs';

import { AuthResult } from '../auth-data/auth-api.service';
import { AuthApiService } from '../auth-data/auth-api.service';
import { MessageResponse } from '../auth-data/auth-api.models';
import {
  FORGOT_PASSWORD_GENERIC_MESSAGE,
  MobileAuthForgotPassword,
} from './mobile-auth-forgot-password';

describe('MobileAuthForgotPassword', () => {
  let requestPasswordReset: ReturnType<
    typeof vi.fn<() => ReturnType<AuthApiService['requestPasswordReset']>>
  >;
  let navigateByUrl: ReturnType<typeof vi.fn<() => Promise<boolean>>>;

  function configure() {
    requestPasswordReset = vi.fn();
    TestBed.configureTestingModule({
      imports: [MobileAuthForgotPassword],
      providers: [
        provideRouter([]),
        { provide: AuthApiService, useValue: { requestPasswordReset } },
      ],
    });
    // RouterLink needs a real Router; spy on it instead of replacing the
    // Router token (see mobile-auth-sign-in.spec.ts for why).
    navigateByUrl = vi
      .spyOn(TestBed.inject(Router), 'navigateByUrl')
      .mockResolvedValue(true) as never;
    const fixture = TestBed.createComponent(MobileAuthForgotPassword);
    fixture.detectChanges();
    return fixture;
  }

  function submitWithEmail(fixture: ReturnType<typeof configure>, email: string) {
    const el = fixture.nativeElement as HTMLElement;
    const input = el.querySelector<HTMLInputElement>('#auth-forgot-email')!;
    input.value = email;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    fixture.detectChanges();
    el.querySelector('form')?.dispatchEvent(
      new Event('submit', { bubbles: true, cancelable: true }),
    );
    fixture.detectChanges();
  }

  it('shows the identical generic message when the account exists', () => {
    const existsFixture = configure();
    requestPasswordReset.mockReturnValue(
      of<AuthResult<MessageResponse>>({
        ok: true,
        value: { message: 'If that email has an account, a reset link has been sent.' },
      }),
    );
    submitWithEmail(existsFixture, 'exists@example.com');
    expect((existsFixture.nativeElement as HTMLElement).textContent).toContain(
      FORGOT_PASSWORD_GENERIC_MESSAGE,
    );
    existsFixture.destroy();
  });

  it('shows the identical generic message when the account does not exist', () => {
    const missingFixture = configure();
    requestPasswordReset.mockReturnValue(
      of<AuthResult<MessageResponse>>({
        ok: true,
        value: { message: 'If that email has an account, a reset link has been sent.' },
      }),
    );
    submitWithEmail(missingFixture, 'does-not-exist@example.com');
    expect((missingFixture.nativeElement as HTMLElement).textContent).toContain(
      FORGOT_PASSWORD_GENERIC_MESSAGE,
    );
    missingFixture.destroy();
  });

  it('never claims an email was sent', () => {
    const fixture = configure();
    requestPasswordReset.mockReturnValue(
      of<AuthResult<MessageResponse>>({ ok: true, value: { message: 'ignored' } }),
    );
    submitWithEmail(fixture, 'a@example.com');
    expect((fixture.nativeElement as HTMLElement).textContent).not.toMatch(/email has been sent/i);
    fixture.destroy();
  });

  it('posts only to /api/auth/password-reset via the typed client', () => {
    const fixture = configure();
    requestPasswordReset.mockReturnValue(
      of<AuthResult<MessageResponse>>({ ok: true, value: { message: 'ignored' } }),
    );
    submitWithEmail(fixture, 'a@example.com');
    expect(requestPasswordReset).toHaveBeenCalledExactlyOnceWith({ email: 'a@example.com' });
    fixture.destroy();
  });

  it('shows the generic rate-limited message distinctly, without leaking account existence', () => {
    const fixture = configure();
    requestPasswordReset.mockReturnValue(
      of<AuthResult<MessageResponse>>({
        ok: false,
        error: { kind: 'rate_limited', message: 'Too many attempts. Try again later.' },
      }),
    );
    submitWithEmail(fixture, 'a@example.com');
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('Too many attempts. Try again later.');
    expect(el.textContent).not.toContain(FORGOT_PASSWORD_GENERIC_MESSAGE);
    fixture.destroy();
  });

  it('navigates to the explicit service-unavailable route when the backend is unreachable', () => {
    const fixture = configure();
    requestPasswordReset.mockReturnValue(
      of<AuthResult<MessageResponse>>({
        ok: false,
        error: { kind: 'unavailable', message: 'Sign-in is temporarily unavailable.' },
      }),
    );
    submitWithEmail(fixture, 'a@example.com');
    expect(navigateByUrl).toHaveBeenCalledExactlyOnceWith('/mobile/auth/service-unavailable');
    fixture.destroy();
  });
});
