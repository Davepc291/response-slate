import { Component, inject, signal } from '@angular/core';
import { Router, RouterLink } from '@angular/router';

import { HEADER_TITLE } from '../../board-preview/board-preview.fixtures';
import { AuthApiService } from '../auth-data/auth-api.service';
import { inputValue } from '../auth-data/input-value';

// Exact wording from Step 9D requirement 6. Deliberately does not claim an
// email was sent, since no delivery provider is approved yet, and is shown
// identically whether or not the submitted address matches an account
// (account-enumeration resistance, docs/authentication-authorization-v1.md
// Section 3).
export const FORGOT_PASSWORD_GENERIC_MESSAGE =
  'If an eligible account matches that information, password-reset instructions will be provided through the approved delivery method.';

/**
 * Forgot-password request screen at /mobile/auth/forgot-password (Step 9D
 * requirement 6). It posts only to POST /api/auth/password-reset and always
 * renders the same public outcome, whether the request succeeded, the
 * account did not exist, or the backend rejected it for a reason other than
 * rate limiting or unavailability — never revealing account existence.
 */
@Component({
  selector: 'app-mobile-auth-forgot-password',
  imports: [RouterLink],
  templateUrl: './mobile-auth-forgot-password.html',
  styleUrl: './mobile-auth-forgot-password.scss',
})
export class MobileAuthForgotPassword {
  private readonly api = inject(AuthApiService);
  private readonly router = inject(Router);

  readonly headerTitle = HEADER_TITLE;
  readonly email = signal('');
  readonly submitting = signal(false);
  readonly errorMessage = signal<string | null>(null);
  readonly successMessage = signal<string | null>(null);
  readonly inputValue = inputValue;

  submit(event: Event): void {
    event.preventDefault();
    if (this.submitting()) {
      return;
    }
    const email = this.email().trim();
    if (!email) {
      this.errorMessage.set('Enter your email address.');
      return;
    }

    this.submitting.set(true);
    this.errorMessage.set(null);
    this.successMessage.set(null);

    this.api.requestPasswordReset({ email }).subscribe((result) => {
      this.submitting.set(false);

      if (!result.ok && result.error.kind === 'unavailable') {
        void this.router.navigateByUrl('/mobile/auth/service-unavailable');
        return;
      }
      if (!result.ok && result.error.kind === 'rate_limited') {
        this.errorMessage.set(result.error.message);
        return;
      }

      // Every other outcome — success or any other failure kind — renders
      // the identical public message, never distinguished.
      this.email.set('');
      this.successMessage.set(FORGOT_PASSWORD_GENERIC_MESSAGE);
    });
  }
}
