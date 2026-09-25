import { Routes } from '@angular/router';

import { requireAdminRole } from './auth-data/require-admin-role.guard';
import { requireVerifiedSession } from './auth-data/require-verified-session.guard';
import { MobileAuthAdminAccessDenied } from './mobile-auth-admin-access-denied/mobile-auth-admin-access-denied';
import { MobileAuthAdminUserDetail } from './mobile-auth-admin-user-detail/mobile-auth-admin-user-detail';
import { MobileAuthAdminUserNew } from './mobile-auth-admin-user-new/mobile-auth-admin-user-new';
import { MobileAuthAdminUsers } from './mobile-auth-admin-users/mobile-auth-admin-users';
import { MobileAuthFirstTimeAccess } from './mobile-auth-first-time-access/mobile-auth-first-time-access';
import { MobileAuthForgotPassword } from './mobile-auth-forgot-password/mobile-auth-forgot-password';
import { MobileAuthMfaEnroll } from './mobile-auth-mfa-enroll/mobile-auth-mfa-enroll';
import { MobileAuthMfaVerify } from './mobile-auth-mfa-verify/mobile-auth-mfa-verify';
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
//
// Step 9E adds explicit, preview-only administrator routes under
// /mobile/auth/admin/**. They are not linked from the existing production
// mobile shell/settings (Step 9E requirement 7) and are not activated as
// production routes until Step 9F. Each is guarded by both
// requireVerifiedSession (existing Step 9D guard) and requireAdminRole (new
// Step 9E navigation-UX guard); the backend's own authorization remains the
// actual boundary. Unknown /mobile/auth/admin/** paths redirect to the user
// list, matching the outer '**' redirect-to-sign-in convention one level
// up. access-denied is deliberately unguarded here so requireAdminRole's
// own redirect target is always reachable without looping.
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
      // Step 9F-5: mfa/enroll is deliberately unguarded — see
      // MobileAuthMfaEnroll's own doc comment for why it must be reachable
      // both with a normal session and with only the narrow MFA-enrollment
      // bridging cookie, neither of which requireVerifiedSession can check
      // (it only ever verifies a normal session). mfa/verify, in contrast,
      // is only ever meaningful for a normal, already-established session
      // (POST /api/auth/mfa/verify accepts nothing else), so it carries the
      // identical guard the sessions preview screen does.
      { path: 'mfa/enroll', component: MobileAuthMfaEnroll },
      { path: 'mfa/verify', component: MobileAuthMfaVerify, canActivate: [requireVerifiedSession] },
      {
        path: 'sessions',
        component: MobileAuthSessions,
        canActivate: [requireVerifiedSession],
      },
      {
        path: 'admin',
        children: [
          { path: '', pathMatch: 'full', redirectTo: 'users' },
          {
            path: 'users',
            component: MobileAuthAdminUsers,
            canActivate: [requireVerifiedSession, requireAdminRole],
          },
          {
            path: 'users/new',
            component: MobileAuthAdminUserNew,
            canActivate: [requireVerifiedSession, requireAdminRole],
          },
          {
            path: 'users/:id',
            component: MobileAuthAdminUserDetail,
            canActivate: [requireVerifiedSession, requireAdminRole],
          },
          { path: 'access-denied', component: MobileAuthAdminAccessDenied },
          { path: '**', redirectTo: 'users' },
        ],
      },
      { path: '**', redirectTo: 'sign-in' },
    ],
  },
];
