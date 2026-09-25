import { Component, computed, inject, signal } from '@angular/core';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';

import { HEADER_TITLE } from '../../board-preview/board-preview.fixtures';
import { AuthApiService } from '../auth-data/auth-api.service';
import {
  assertionCredentialToJSON,
  isPublicKeyCredential,
  isValidRequestOptions,
  isWebAuthnSupported,
  toRequestOptions,
} from '../auth-data/webauthn';

type VerifyPhase = 'idle' | 'starting' | 'awaiting-device' | 'finishing';

// Where a caller lands once verification succeeds, absent a `returnUrl`
// query param (mfa-required.interceptor.ts sets one when it redirects here
// from a blocked /api/admin/* call) — see navigateAfterSuccess.
const DEFAULT_RETURN_URL = '/mobile/auth/admin/users';

const BUTTON_LABEL: Record<VerifyPhase, string> = {
  idle: 'Verify passkey',
  starting: 'Starting…',
  'awaiting-device': 'Waiting for your device…',
  finishing: 'Finishing…',
};

/**
 * MFA (passkey) verification screen at /mobile/auth/mfa/verify (Step
 * 9F-5), guarded by requireVerifiedSession: POST /api/auth/mfa/verify only
 * ever accepts a normal, already-established session (requireSession in
 * backend/internal/authhttp/routes.go — never the enrollment bridging
 * cookie), so there is nothing this screen can do for a caller with no
 * session at all. Reached either directly (an administrator choosing to
 * verify) or via mfa-required.interceptor.ts's redirect after a blocked
 * /api/admin/* request. Never stores a WebAuthn ceremony value (challenge,
 * assertion, credential id) anywhere but the in-flight Promise/Observable
 * chain that produced it.
 */
@Component({
  selector: 'app-mobile-auth-mfa-verify',
  imports: [RouterLink],
  templateUrl: './mobile-auth-mfa-verify.html',
  styleUrl: './mobile-auth-mfa-verify.scss',
})
export class MobileAuthMfaVerify {
  private readonly api = inject(AuthApiService);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);

  readonly headerTitle = HEADER_TITLE;
  readonly supported = signal(isWebAuthnSupported());
  readonly phase = signal<VerifyPhase>('idle');
  readonly errorMessage = signal<string | null>(null);
  readonly busy = computed(() => this.phase() !== 'idle');
  readonly buttonLabel = computed(() => BUTTON_LABEL[this.phase()]);

  startVerification(): void {
    if (this.busy() || !this.supported()) {
      return;
    }
    this.errorMessage.set(null);
    this.phase.set('starting');

    this.api.mfaVerifyBegin().subscribe((beginResult) => {
      if (!beginResult.ok) {
        this.phase.set('idle');
        this.handleFailure(beginResult.error.kind, beginResult.error.message);
        return;
      }
      if (!isValidRequestOptions(beginResult.value)) {
        this.phase.set('idle');
        this.errorMessage.set('Verification could not be started. Try again.');
        return;
      }

      this.phase.set('awaiting-device');
      navigator.credentials
        .get(toRequestOptions(beginResult.value))
        .then((credential) => this.finishVerification(credential))
        .catch(() => {
          this.phase.set('idle');
          this.errorMessage.set(
            'Passkey verification was cancelled or could not be completed. Try again.',
          );
        });
    });
  }

  private finishVerification(credential: Credential | null): void {
    if (!isPublicKeyCredential(credential)) {
      this.phase.set('idle');
      this.errorMessage.set('Passkey verification could not be completed. Try again.');
      return;
    }
    this.phase.set('finishing');
    this.api.mfaVerifyFinish(assertionCredentialToJSON(credential)).subscribe((finishResult) => {
      this.phase.set('idle');
      if (!finishResult.ok) {
        this.handleFailure(finishResult.error.kind, finishResult.error.message);
        return;
      }
      this.navigateAfterSuccess();
    });
  }

  private handleFailure(kind: string, message: string): void {
    if (kind === 'unavailable') {
      void this.router.navigateByUrl('/mobile/auth/service-unavailable');
      return;
    }
    if (kind === 'not_authenticated' || kind === 'session_expired') {
      void this.router.navigateByUrl('/mobile/auth/sign-in');
      return;
    }
    this.errorMessage.set(message);
  }

  private navigateAfterSuccess(): void {
    const requested = this.route.snapshot.queryParamMap.get('returnUrl');
    const target =
      requested && requested.startsWith('/mobile/auth/admin/') ? requested : DEFAULT_RETURN_URL;
    void this.router.navigateByUrl(target);
  }
}
