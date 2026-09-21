import { Component, computed, inject, signal } from '@angular/core';
import { Router, RouterLink } from '@angular/router';

import { HEADER_TITLE } from '../../board-preview/board-preview.fixtures';
import { AuthApiService } from '../auth-data/auth-api.service';
import { inputValue } from '../auth-data/input-value';

/**
 * First-time-access screen at /mobile/auth/first-time-access (Step 9D
 * requirement 5). The invitation code is typed/pasted into a form field and
 * sent only in the JSON body of POST /api/auth/first-time-login — never in
 * the URL, a query string, or browser storage. Password confirmation is
 * validated locally as a UX convenience only; the backend remains
 * authoritative for whether the new password is accepted.
 */
@Component({
  selector: 'app-mobile-auth-first-time-access',
  imports: [RouterLink],
  templateUrl: './mobile-auth-first-time-access.html',
  styleUrl: './mobile-auth-first-time-access.scss',
})
export class MobileAuthFirstTimeAccess {
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
  readonly verificationNeeded = signal(false);
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
    this.verificationNeeded.set(false);

    const code = this.code().trim();
    const password = this.newPassword();
    if (!code || !password) {
      this.errorMessage.set('Enter your invitation code and a new password.');
      return;
    }
    if (this.mismatch()) {
      this.errorMessage.set('Passwords do not match.');
      return;
    }

    this.submitting.set(true);
    this.api.firstTimeLogin({ token: code, password }).subscribe((result) => {
      // The code and both password fields are cleared once the response is
      // handled — on success or on a terminal failure alike — never before.
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

      if (result.value.status !== 'active') {
        // The account still requires further verification (for example, an
        // administrator role requiring MFA enrollment this preview does not
        // implement). Show a safe generic state; never bypass or fabricate
        // an MFA flow.
        this.verificationNeeded.set(true);
        return;
      }

      this.successMessage.set('Your password has been set. You can now sign in.');
    });
  }
}
