import { Routes } from '@angular/router';

import { BoardPreview } from './board-preview/board-preview';
import { mobileAuthRoutes } from './mobile-auth/mobile-auth.routes';
import { mobileRoutes } from './mobile/mobile.routes';

// 'mobile/auth' is listed before the generic 'mobile' entry: mobile.routes.ts
// (unchanged by Step 9D) has its own '**' wildcard child that redirects any
// unmatched /mobile/* path to /mobile/welcome, so it must not be tried
// first or it would swallow every /mobile/auth/** URL before the auth
// routes below ever get a chance to match.
export const routes: Routes = [
  { path: '', component: BoardPreview, pathMatch: 'full' },
  { path: 'mobile/auth', children: mobileAuthRoutes },
  { path: 'mobile', children: mobileRoutes },
];
