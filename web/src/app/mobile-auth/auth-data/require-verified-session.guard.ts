import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { map } from 'rxjs';

import { AuthApiService } from './auth-api.service';

/**
 * Route guard originally added for the /mobile/auth/sessions preview screen
 * (Step 9D requirement 9: "must require a successfully verified
 * /api/auth/me session") and reused, unchanged, by Step 9F-7 production
 * activation for the protected mobile content routes (/mobile/home,
 * /calls, /units, /evidence, /settings). app.routes.ts's '/' entry uses the
 * sibling redirect-root.guard.ts instead — Angular does not allow
 * combining canActivate with redirectTo on the same route — but that guard
 * performs the identical GET /api/auth/me check via the same
 * AuthApiService. The backend remains the actual authorization boundary —
 * this guard only re-verifies against a fresh GET /api/auth/me on every
 * activation and sends an unverified caller to sign-in, matching the
 * session-state foundation's own "server is the source of truth" rule
 * rather than trusting any cached client state.
 */
export const requireVerifiedSession: CanActivateFn = () => {
  const api = inject(AuthApiService);
  const router = inject(Router);

  return api
    .me()
    .pipe(map((result) => (result.ok ? true : router.createUrlTree(['/mobile/auth/sign-in']))));
};
