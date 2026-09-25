import { Routes } from '@angular/router';

import { BoardPreview } from './board-preview/board-preview';
import { redirectRoot } from './mobile-auth/auth-data/redirect-root.guard';
import { mobileAuthRoutes } from './mobile-auth/mobile-auth.routes';
import { mobileRoutes } from './mobile/mobile.routes';

// 'mobile/auth' is listed before the generic 'mobile' entry: mobile.routes.ts
// has its own '**' wildcard child that redirects any unmatched /mobile/*
// path to /mobile/welcome, so it must not be tried first or it would
// swallow every /mobile/auth/** URL before the auth routes below ever get
// a chance to match.
//
// Step 9F-7 production activation: '' never renders a component. redirectRoot
// (built on the same AuthApiService.me() check requireVerifiedSession uses
// for /mobile/auth/sessions and /mobile/auth/admin/**) sends an
// unauthenticated visitor to sign-in and an authenticated one into the
// protected mobile area at /mobile/home. The synthetic SHADOW/REPLAY board
// that used to live at '/' moves to the explicit '/preview' path — still
// reachable for testing/replay, no longer the production landing page.
export const routes: Routes = [
  { path: '', pathMatch: 'full', canActivate: [redirectRoot], children: [] },
  { path: 'preview', component: BoardPreview },
  { path: 'mobile/auth', children: mobileAuthRoutes },
  { path: 'mobile', children: mobileRoutes },
];
