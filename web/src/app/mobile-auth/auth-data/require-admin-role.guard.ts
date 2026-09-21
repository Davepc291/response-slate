import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { map } from 'rxjs';

import { AuthApiService } from './auth-api.service';

const ADMIN_ROLES = new Set(['system_administrator', 'department_administrator']);

/**
 * Client-side navigation-UX guard for the /mobile/auth/admin/** preview
 * screens (Step 9E requirement 7: "Add a client-side role/permission guard
 * for navigation UX"). This is UX only, never the authorization boundary:
 * the backend's own authorization.Allowed(ManageUsers) check on every
 * /api/admin/* request (docs/authentication-authorization-v1.md Section 6)
 * remains authoritative regardless of what this guard decides, exactly as
 * requireVerifiedSession's own doc comment already states for the sessions
 * preview screen. Always applied alongside requireVerifiedSession, never
 * instead of it: this guard performs its own fresh GET /api/auth/me call
 * (never trusting cached client state) and only additionally checks role,
 * so it is safe to compose in either order.
 */
export const requireAdminRole: CanActivateFn = () => {
  const api = inject(AuthApiService);
  const router = inject(Router);

  return api.me().pipe(
    map((result) => {
      if (result.ok && ADMIN_ROLES.has(result.value.role)) {
        return true;
      }
      if (!result.ok && result.error.kind === 'not_authenticated') {
        return router.createUrlTree(['/mobile/auth/sign-in']);
      }
      return router.createUrlTree(['/mobile/auth/admin/access-denied']);
    }),
  );
};
