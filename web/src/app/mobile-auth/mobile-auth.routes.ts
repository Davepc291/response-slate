import { Routes } from '@angular/router';

import { requireVerifiedSession } from './auth-data/require-verified-session.guard';
import { MobileAuthFirstTimeAccess } from './mobile-auth-first-time-access/mobile-auth-first-time-access';
import { MobileAuthForgotPassword } from './mobile-auth-forgot-password/mobile-auth-forgot-password';
import { MobileAuthResetPassword } from './mobile-auth-reset-password/mobile-auth-reset-password';
import { MobileAuthServiceUnavailable } from './mobile-auth-service-unavailable/mobile-auth-service-unavailable';
import { MobileAuthSessions } from './mobile-auth-sessions/mobile-auth-sessions';
import { MobileAuthShell } from './mobile-auth-shell/mobile-auth-shell';
import { MobileAuthSignIn } from './mobile-auth-sign-in/mobile-auth-sign-in';

// Step 9D preview routes under /mobile/auth/**. None of these routes is the
// production mobile entry point yet (see mobile-auth-shell.ts's doc
// comment): production activation is Step 9F's, after the backend is
// deployed at the same origin. Unknown /mobile/auth/** paths redirect to
// sign-in, matching mobile.routes.ts's own unknown-path convention.
export const mobileAuthRoutes: Routes = [
  {
    path: '',
    component: MobileAuthShell,
    children: [
      { path: '', pathMatch: 'full', redirectTo: 'sign-in' },
      { path: 'sign-in', component: MobileAuthSignIn },
      { path: 'first-time-access', component: MobileAuthFirstTimeAccess },
      { path: 'forgot-password', component: MobileAuthForgotPassword },
      { path: 'reset-password', component: MobileAuthResetPassword },
      { path: 'service-unavailable', component: MobileAuthServiceUnavailable },
      {
        path: 'sessions',
        component: MobileAuthSessions,
        canActivate: [requireVerifiedSession],
      },
      { path: '**', redirectTo: 'sign-in' },
    ],
  },
];
