import { TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { of } from 'rxjs';

import { AuthResult } from '../auth-data/auth-api.service';
import { AuthApiService } from '../auth-data/auth-api.service';
import { StatusResponse } from '../auth-data/auth-api.models';
import { MobileAuthFirstTimeAccess } from './mobile-auth-first-time-access';

describe('MobileAuthFirstTimeAccess', () => {
  let firstTimeLogin: ReturnType<typeof vi.fn<() => ReturnType<AuthApiService['firstTimeLogin']>>>;
  let navigateByUrl: ReturnType<typeof vi.fn<() => Promise<boolean>>>;

  function configure() {
    firstTimeLogin = vi.fn();
    TestBed.configureTestingModule({
      imports: [MobileAuthFirstTimeAccess],
      providers: [provideRouter([]), { provide: AuthApiService, useValue: { firstTimeLogin } }],
    });
    // RouterLink needs a real Router; spy on it instead of replacing the
    // Router token (see mobile-auth-sign-in.spec.ts for why).
    navigateByUrl = vi
      .spyOn(TestBed.inject(Router), 'navigateByUrl')
      .mockResolvedValue(true) as never;
    const fixture = TestBed.createComponent(MobileAuthFirstTimeAccess);
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

  it('explains single-use and expiring invitation semantics', () => {
    const fixture = configure();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('single-use');
    expect(el.textContent).toMatch(/expire/i);
    fixture.destroy();
  });

  it('rejects a mismatched confirmation locally without calling the API', () => {
    const fixture = configure();
    setValue(fixture, '#auth-fta-code', 'invite-code');
    setValue(fixture, '#auth-fta-password', 'a long passphrase here');
    setValue(fixture, '#auth-fta-confirm', 'a different passphrase');
    submit(fixture);

    expect(firstTimeLogin).not.toHaveBeenCalled();
    expect((fixture.nativeElement as HTMLElement).textContent).toContain('Passwords do not match.');
    fixture.destroy();
  });

  it('sends the code only in the request payload, never rendering it into the DOM as a link/URL', () => {
    const fixture = configure();
    firstTimeLogin.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'active' } }),
    );
    setValue(fixture, '#auth-fta-code', 'super-secret-invite-token');
    setValue(fixture, '#auth-fta-password', 'a long passphrase here');
    setValue(fixture, '#auth-fta-confirm', 'a long passphrase here');
    submit(fixture);

    expect(firstTimeLogin).toHaveBeenCalledExactlyOnceWith({
      token: 'super-secret-invite-token',
      password: 'a long passphrase here',
    });
    const el = fixture.nativeElement as HTMLElement;
    expect(el.innerHTML).not.toContain('super-secret-invite-token');
    fixture.destroy();
  });

  it('clears the code and both password fields after a terminal failure', () => {
    const fixture = configure();
    firstTimeLogin.mockReturnValue(
      of<AuthResult<StatusResponse>>({
        ok: false,
        error: {
          kind: 'invitation_expired',
          message: 'This invitation has expired. Ask your administrator to resend it.',
        },
      }),
    );
    setValue(fixture, '#auth-fta-code', 'expired-code');
    setValue(fixture, '#auth-fta-password', 'a long passphrase here');
    setValue(fixture, '#auth-fta-confirm', 'a long passphrase here');
    submit(fixture);

    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('This invitation has expired.');
    expect(el.querySelector<HTMLInputElement>('#auth-fta-code')?.value).toBe('');
    expect(el.querySelector<HTMLInputElement>('#auth-fta-password')?.value).toBe('');
    expect(el.querySelector<HTMLInputElement>('#auth-fta-confirm')?.value).toBe('');
    fixture.destroy();
  });

  it('shows a safe additional-verification message rather than bypassing or fabricating MFA', () => {
    const fixture = configure();
    firstTimeLogin.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'password_change_required' } }),
    );
    setValue(fixture, '#auth-fta-code', 'invite-code');
    setValue(fixture, '#auth-fta-password', 'a long passphrase here');
    setValue(fixture, '#auth-fta-confirm', 'a long passphrase here');
    submit(fixture);

    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('Additional verification is required');
    expect(el.textContent).not.toMatch(/enroll|scan this code|authenticator app/i);
    fixture.destroy();
  });

  it('shows a completion message with a link to sign in on success, without auto-navigating away', () => {
    const fixture = configure();
    firstTimeLogin.mockReturnValue(
      of<AuthResult<StatusResponse>>({ ok: true, value: { status: 'active' } }),
    );
    setValue(fixture, '#auth-fta-code', 'invite-code');
    setValue(fixture, '#auth-fta-password', 'a long passphrase here');
    setValue(fixture, '#auth-fta-confirm', 'a long passphrase here');
    submit(fixture);

    const el = fixture.nativeElement as HTMLElement;
    expect(el.textContent).toContain('Your password has been set.');
    expect(el.querySelector('a[href="/mobile/auth/sign-in"]')).not.toBeNull();
    expect(navigateByUrl).not.toHaveBeenCalled();
    fixture.destroy();
  });

  it('handles a rate-limited attempt with the generic locked message', () => {
    const fixture = configure();
    firstTimeLogin.mockReturnValue(
      of<AuthResult<StatusResponse>>({
        ok: false,
        error: { kind: 'rate_limited', message: 'Too many attempts. Try again later.' },
      }),
    );
    setValue(fixture, '#auth-fta-code', 'invite-code');
    setValue(fixture, '#auth-fta-password', 'a long passphrase here');
    setValue(fixture, '#auth-fta-confirm', 'a long passphrase here');
    submit(fixture);

    expect((fixture.nativeElement as HTMLElement).textContent).toContain(
      'Too many attempts. Try again later.',
    );
    fixture.destroy();
  });
});
