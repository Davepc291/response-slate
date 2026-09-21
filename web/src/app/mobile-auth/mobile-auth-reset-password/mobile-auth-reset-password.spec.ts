import { TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { of } from 'rxjs';

import { AuthResult } from '../auth-data/auth-api.service';
import { AuthApiService } from '../auth-data/auth-api.service';
import { StatusResponse } from '../auth-data/auth-api.models';
import { MobileAuthResetPassword } from './mobile-auth-reset-password';

describe('MobileAuthResetPassword', () => {
  let completePasswordReset: ReturnType<
    typeof vi.fn<() => ReturnType<AuthApiService['completePasswordReset']>>
  >;
  let navigateByUrl: ReturnType<typeof vi.fn<() => Promise<boolean>>>;

  function configure() {
    completePasswordReset = vi.fn();
    TestBed.configureTestingModule({
      imports: [MobileAuthResetPassword],
      providers: [
        provideRouter([]),
        { provide: AuthApiService, useValue: { completePasswordReset } },
      ],
    });
    // RouterLink needs a real Router; spy on it instead of replacing the
    // Router token (see mobile-auth-sign-in.spec.ts for why).
    navigateByUrl = vi
      .spyOn(TestBed.inject(Router), 'navigateByUrl')
      .mockResolvedValue(true) as never;
    const fixture = TestBed.createComponent(MobileAuthResetPassword);
    fixture.detectChanges();
    return fixture;
  }

  function setValue(fixture: ReturnType<typeof configure>, id: string, value: string) {
    const el = fixture.nativeElement as HTMLElement;
    const input = el.querySelector<HTMLInputElement>(id)!;
    input.value = value;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    fixture.detectChanges();
  }

  function submit(fixture: ReturnType<typeof configure>) {
    (fixture.nativeElement as HTMLElement)
      .querySelector('form')
      ?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    fixture.detectChanges();
  }

  it('rejects a mismatched confirmation locally without calling the API', () => {
    const fixture = configure();
    setValue(fixture, '#auth-reset-code', 'reset-code');
    setValue(fixture, '#auth-reset-password', 'a long passphrase here');
    setValue(fixture, '#auth-reset-confirm', 'not the same');
    submit(fixture);

    expect(completePasswordReset).not.toHaveBeenCalled();
    fixture.destroy();
  });

  it('sends the reset code only in the request payload, never into the DOM as a link', () => {
    const fixture = configure();
    completePasswordReset.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'password_reset' } }),
    );
    setValue(fixture, '#auth-reset-code', 'super-secret-reset-token');
    setValue(fixture, '#auth-reset-password', 'a long passphrase here');
    setValue(fixture, '#auth-reset-confirm', 'a long passphrase here');
    submit(fixture);

    expect(completePasswordReset).toHaveBeenCalledExactlyOnceWith({
      token: 'super-secret-reset-token',
      password: 'a long passphrase here',
    });
    expect((fixture.nativeElement as HTMLElement).innerHTML).not.toContain(
      'super-secret-reset-token',
    );
    fixture.destroy();
  });

  it('explains session revocation and links to sign in on success', () => {
    const fixture = configure();
    completePasswordReset.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'password_reset' } }),
    );
    setValue(fixture, '#auth-reset-code', 'reset-code');
    setValue(fixture, '#auth-reset-password', 'a long passphrase here');
    setValue(fixture, '#auth-reset-confirm', 'a long passphrase here');
    submit(fixture);

    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toMatch(/session.*revoked|revoked.*session/i);
    expect(el.querySelector('a[href="/mobile/auth/sign-in"]')).not.toBeNull();
    fixture.destroy();
  });

  it('clears the code and passwords after an expired/invalid/reused-code failure', () => {
    const fixture = configure();
    completePasswordReset.mockReturnValue(
      of<AuthResult<StatusResponse>>({
        ok: false,
        error: {
          kind: 'reset_link_invalid',
          message: 'This password reset link is invalid or has already been used.',
        },
      }),
    );
    setValue(fixture, '#auth-reset-code', 'reused-code');
    setValue(fixture, '#auth-reset-password', 'a long passphrase here');
    setValue(fixture, '#auth-reset-confirm', 'a long passphrase here');
    submit(fixture);

    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('invalid or has already been used');
    expect(el.querySelector<HTMLInputElement>('#auth-reset-code')?.value).toBe('');
    expect(el.querySelector<HTMLInputElement>('#auth-reset-password')?.value).toBe('');
    expect(el.querySelector<HTMLInputElement>('#auth-reset-confirm')?.value).toBe('');
    fixture.destroy();
  });

  it('navigates to the explicit service-unavailable route when unavailable', () => {
    const fixture = configure();
    completePasswordReset.mockReturnValue(
      of<AuthResult<StatusResponse>>({
        ok: false,
        error: { kind: 'unavailable', message: 'Sign-in is temporarily unavailable.' },
      }),
    );
    setValue(fixture, '#auth-reset-code', 'reset-code');
    setValue(fixture, '#auth-reset-password', 'a long passphrase here');
    setValue(fixture, '#auth-reset-confirm', 'a long passphrase here');
    submit(fixture);

    expect(navigateByUrl).toHaveBeenCalledExactlyOnceWith('/mobile/auth/service-unavailable');
    fixture.destroy();
  });
});
