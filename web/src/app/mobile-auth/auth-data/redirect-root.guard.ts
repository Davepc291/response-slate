import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { map } from 'rxjs';

import { AuthApiService } from './auth-api.service';

/**
 * Route guard for app.routes.ts's '/' entry only (Step 9F-7 production
 * activation). '/' never renders a component of its own: Angular's router
 * rejects combining `canActivate` with `redirectTo` on the same route
 * ("redirects happen before guards are executed"), so this guard performs
 * the redirect itself instead, via the identical GET /api/auth/me check
 * requireVerifiedSession already uses elsewhere (same AuthApiService call,
 * same "server is the source of truth" rule) — it only differs in what it
 * does with a successful result: an authenticated visitor is sent into the
 * protected mobile area at /mobile/home, an unauthenticated one to
 * /mobile/auth/sign-in, exactly where requireVerifiedSession would have
 * sent it too.
 */
export const redirectRoot: CanActivateFn = () => {
  const api = inject(AuthApiService);
  const router = inject(Router);

  return api
    .me()
    .pipe(map((result) => router.createUrlTree([result.ok ? '/mobile/home' : '/mobile/auth/sign-in'])));
};
