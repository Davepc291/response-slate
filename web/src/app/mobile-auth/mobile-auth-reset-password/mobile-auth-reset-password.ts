import { Component, computed, inject, signal } from '@angular/core';
import { Router, RouterLink } from '@angular/router';

import { HEADER_TITLE } from '../../board-preview/board-preview.fixtures';
import { AuthApiService } from '../auth-data/auth-api.service';
import { inputValue } from '../auth-data/input-value';

/**
 * Reset-password completion screen at /mobile/auth/reset-password (Step 9D
 * requirement 7). The reset code is entered into a form field and sent only
 * in the JSON body of POST /api/auth/password-reset/complete — never in a
 * URL. A successful completion revokes every existing session for the
 * account server-side (backend/internal/identityservice.CompletePasswordReset);
 * this screen states that fact rather than re-deriving it.
 */
@Component({
  selector: 'app-mobile-auth-reset-password',
  imports: [RouterLink],
  templateUrl: './mobile-auth-reset-password.html',
  styleUrl: './mobile-auth-reset-password.scss',
})
export class MobileAuthResetPassword {
  private readonly api = inject(AuthApiService);
  private readonly router = inject(Router);

  readonly headerTitle = HEADER_TITLE;
  readonly code = signal('');
  readonly newPassword = signal('');
  readonly confirmPassword = signal('');
  readonly passwordVisible = signal(false);
  readonly submitting = signal(false);
  readonly errorMessage = signal<string | null>(null);
  readonly successMessage = signal<string | null>(null);
  readonly passwordFieldType = computed(() => (this.passwordVisible() ? 'text' : 'password'));
  readonly mismatch = computed(
    () => this.confirmPassword().length > 0 && this.newPassword() !== this.confirmPassword(),
  );
  readonly inputValue = inputValue;

  togglePasswordVisibility(): void {
    this.passwordVisible.update((visible) => !visible);
  }

  submit(event: Event): void {
    event.preventDefault();
    if (this.submitting()) {
      return;
    }
    this.errorMessage.set(null);
    this.successMessage.set(null);

    const code = this.code().trim();
    const password = this.newPassword();
    if (!code || !password) {
      this.errorMessage.set('Enter your reset code and a new password.');
      return;
    }
    if (this.mismatch()) {
      this.errorMessage.set('Passwords do not match.');
      return;
    }

    this.submitting.set(true);
    this.api.completePasswordReset({ token: code, password }).subscribe((result) => {
      this.code.set('');
      this.newPassword.set('');
      this.confirmPassword.set('');
      this.submitting.set(false);

      if (!result.ok) {
        if (result.error.kind === 'unavailable') {
          void this.router.navigateByUrl('/mobile/auth/service-unavailable');
          return;
        }
        this.errorMessage.set(result.error.message);
        return;
      }

      this.successMessage.set(
        'Your password has been reset. Every existing session for this account has been revoked.',
      );
    });
  }
}
