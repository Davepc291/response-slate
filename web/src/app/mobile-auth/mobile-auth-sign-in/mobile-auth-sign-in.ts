import { Component, computed, inject, signal } from '@angular/core';
import { Router, RouterLink } from '@angular/router';

import { HEADER_TITLE } from '../../board-preview/board-preview.fixtures';
import { AuthApiService } from '../auth-data/auth-api.service';
import { AuthSessionState } from '../auth-data/auth-session-state.service';
import { inputValue } from '../auth-data/input-value';

/**
 * Real sign-in screen at /mobile/auth/sign-in (Step 9D requirement 4). It
 * submits only to POST /api/auth/login, never fabricates a successful
 * login, and always verifies GET /api/auth/me before treating the session
 * as established. Step 9F-7 activates production routing: a successful
 * sign-in now navigates into the protected mobile area at /mobile/home
 * (the same route app.routes.ts's guarded '/' redirects an already
 * authenticated visitor to), not the /mobile/auth/sessions preview screen.
 */
@Component({
  selector: 'app-mobile-auth-sign-in',
  imports: [RouterLink],
  templateUrl: './mobile-auth-sign-in.html',
  styleUrl: './mobile-auth-sign-in.scss',
})
export class MobileAuthSignIn {
  private readonly api = inject(AuthApiService);
  private readonly session = inject(AuthSessionState);
  private readonly router = inject(Router);

  readonly headerTitle = HEADER_TITLE;
  readonly email = signal('');
  readonly password = signal('');
  readonly passwordVisible = signal(false);
  readonly submitting = signal(false);
  readonly errorMessage = signal<string | null>(null);
  readonly passwordFieldType = computed(() => (this.passwordVisible() ? 'text' : 'password'));
  readonly inputValue = inputValue;

  togglePasswordVisibility(): void {
    this.passwordVisible.update((visible) => !visible);
  }

  submit(event: Event): void {
    event.preventDefault();
    if (this.submitting()) {
      return;
    }
    const email = this.email().trim();
    const password = this.password();
    if (!email || !password) {
      this.errorMessage.set('Enter your email and password.');
      return;
    }

    this.submitting.set(true);
    this.errorMessage.set(null);

    this.api.login({ email, password }).subscribe((loginResult) => {
      if (!loginResult.ok) {
        // The password is only cleared once the response is handled, never
        // before, so a resubmitted request never races an in-flight one.
        this.password.set('');
        this.submitting.set(false);
        if (loginResult.error.kind === 'unavailable') {
          void this.router.navigateByUrl('/mobile/auth/service-unavailable');
          return;
        }
        this.errorMessage.set(loginResult.error.message);
        return;
      }

      // A 200 from /login is not enough on its own: verify the session by
      // calling /api/auth/me before treating it as established.
      this.api.me().subscribe((meResult) => {
        this.password.set('');
        this.submitting.set(false);
        if (!meResult.ok) {
          this.errorMessage.set('Sign-in is temporarily unavailable.');
          return;
        }
        this.session.setAuthenticated(meResult.value);
        void this.router.navigateByUrl('/mobile/home');
      });
    });
  }
}
