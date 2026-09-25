import { Component, computed, inject, signal } from '@angular/core';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';

import { HEADER_TITLE } from '../../board-preview/board-preview.fixtures';
import { AuthApiService } from '../auth-data/auth-api.service';
import {
  isPublicKeyCredential,
  isValidCreationOptions,
  isWebAuthnSupported,
  registrationCredentialToJSON,
  toCreationOptions,
} from '../auth-data/webauthn';

type EnrollPhase = 'idle' | 'starting' | 'awaiting-device' | 'finishing';

// Default landing spot once an already-signed-in caller (adding another
// passkey to an active account) finishes enrollment. A `returnUrl` query
// param, when present and scoped to /mobile/auth/, is preferred instead —
// see navigateAfterSuccess.
const DEFAULT_AUTHENTICATED_RETURN_URL = '/mobile/auth/sessions';

const BUTTON_LABEL: Record<EnrollPhase, string> = {
  idle: 'Set up passkey',
  starting: 'Starting…',
  'awaiting-device': 'Waiting for your device…',
  finishing: 'Finishing…',
};

/**
 * MFA (passkey) enrollment screen at /mobile/auth/mfa/enroll (Step 9F-5).
 * Deliberately unguarded by requireVerifiedSession: it must be reachable
 * both by an already-signed-in caller adding another passkey (normal
 * session cookie) and by a password_change_required administrator who has
 * no normal session yet, only the narrow __Host-gfr_mfa_enroll bridging
 * cookie backend/internal/authhttp/invitation.go issues after first-time
 * password establishment (backend/internal/identityservice/mfa.go's own
 * doc comment explains why no session can exist yet for that account). The
 * browser sends whichever cookie it has automatically; this component never
 * reads or distinguishes the cookie value itself, and never stores any
 * WebAuthn ceremony value (challenge, attestation, credential id) anywhere
 * but the in-flight Promise/Observable chain that produced it.
 */
@Component({
  selector: 'app-mobile-auth-mfa-enroll',
  imports: [RouterLink],
  templateUrl: './mobile-auth-mfa-enroll.html',
  styleUrl: './mobile-auth-mfa-enroll.scss',
})
export class MobileAuthMfaEnroll {
  private readonly api = inject(AuthApiService);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);

  readonly headerTitle = HEADER_TITLE;
  readonly supported = signal(isWebAuthnSupported());
  // Distinguishes the two reachable contexts (doc comment above) so a
  // successful finish knows where to go: determined by an actual
  // GET /api/auth/me call, never assumed, since only a normal session
  // authenticates that route (requireSession) — the bridging cookie alone
  // never will.
  private readonly hasActiveSession = signal(false);
  readonly phase = signal<EnrollPhase>('idle');
  readonly errorMessage = signal<string | null>(null);
  readonly busy = computed(() => this.phase() !== 'idle');
  readonly buttonLabel = computed(() => BUTTON_LABEL[this.phase()]);

  constructor() {
    this.api.me().subscribe((result) => {
      this.hasActiveSession.set(result.ok);
    });
  }

  startEnrollment(): void {
    if (this.busy() || !this.supported()) {
      return;
    }
    this.errorMessage.set(null);
    this.phase.set('starting');

    this.api.mfaEnrollBegin().subscribe((beginResult) => {
      if (!beginResult.ok) {
        this.phase.set('idle');
        this.handleFailure(beginResult.error.kind, beginResult.error.message);
        return;
      }
      if (!isValidCreationOptions(beginResult.value)) {
        this.phase.set('idle');
        this.errorMessage.set('Enrollment could not be started. Try again.');
        return;
      }

      this.phase.set('awaiting-device');
      navigator.credentials
        .create(toCreationOptions(beginResult.value))
        .then((credential) => this.finishEnrollment(credential))
        .catch(() => {
          this.phase.set('idle');
          this.errorMessage.set(
            'Passkey setup was cancelled or could not be completed. Try again.',
          );
        });
    });
  }

  private finishEnrollment(credential: Credential | null): void {
    if (!isPublicKeyCredential(credential)) {
      this.phase.set('idle');
      this.errorMessage.set('Passkey setup could not be completed. Try again.');
      return;
    }
    this.phase.set('finishing');
    this.api.mfaEnrollFinish(registrationCredentialToJSON(credential)).subscribe((finishResult) => {
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
    if (kind === 'not_authenticated' && !this.hasActiveSession()) {
      // Neither a normal session nor a still-valid bridging cookie was
      // presented: the first-time enrollment window has expired.
      this.errorMessage.set(
        'This enrollment link has expired. Ask your administrator for a new invitation.',
      );
      return;
    }
    this.errorMessage.set(message);
  }

  private navigateAfterSuccess(): void {
    if (!this.hasActiveSession()) {
      void this.router.navigateByUrl('/mobile/auth/sign-in');
      return;
    }
    const requested = this.route.snapshot.queryParamMap.get('returnUrl');
    const target =
      requested && requested.startsWith('/mobile/auth/')
        ? requested
        : DEFAULT_AUTHENTICATED_RETURN_URL;
    void this.router.navigateByUrl(target);
  }
}
