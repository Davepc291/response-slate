import { Routes } from '@angular/router';

import { requireVerifiedSession } from '../mobile-auth/auth-data/require-verified-session.guard';
import { MobileCalls } from './mobile-calls/mobile-calls';
import { MobileEvidence } from './mobile-evidence/mobile-evidence';
import { MobileHome } from './mobile-home/mobile-home';
import { MobileSettings } from './mobile-settings/mobile-settings';
import { MobileShell } from './mobile-shell/mobile-shell';
import { MobileUnits } from './mobile-units/mobile-units';
import { MobileWelcome } from './mobile-welcome/mobile-welcome';

// Step 9F-7 production activation: the actual content screens (home, calls,
// units, evidence, settings) are the protected mobile area app.routes.ts's
// guarded '/' redirects authenticated visitors into, so each one carries
// the same requireVerifiedSession guard already used for
// /mobile/auth/sessions and /mobile/auth/admin/**. 'welcome' stays
// unguarded: it is a data-free landing/orientation screen (matching the
// unguarded /mobile/auth/service-unavailable and mfa/enroll screens' own
// precedent), and the '**' wildcard's redirect target must remain reachable
// without looping.
export const mobileRoutes: Routes = [
  {
    path: '',
    component: MobileShell,
    children: [
      { path: '', pathMatch: 'full', redirectTo: 'welcome' },
      { path: 'welcome', component: MobileWelcome },
      { path: 'home', component: MobileHome, canActivate: [requireVerifiedSession] },
      { path: 'calls', component: MobileCalls, canActivate: [requireVerifiedSession] },
      { path: 'units', component: MobileUnits, canActivate: [requireVerifiedSession] },
      { path: 'evidence', component: MobileEvidence, canActivate: [requireVerifiedSession] },
      { path: 'settings', component: MobileSettings, canActivate: [requireVerifiedSession] },
      { path: '**', redirectTo: 'welcome' },
    ],
  },
];
