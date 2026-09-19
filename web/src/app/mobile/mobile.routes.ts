import { Routes } from '@angular/router';

import { MobileCalls } from './mobile-calls/mobile-calls';
import { MobileEvidence } from './mobile-evidence/mobile-evidence';
import { MobileHome } from './mobile-home/mobile-home';
import { MobileSettings } from './mobile-settings/mobile-settings';
import { MobileShell } from './mobile-shell/mobile-shell';
import { MobileUnits } from './mobile-units/mobile-units';
import { MobileWelcome } from './mobile-welcome/mobile-welcome';

export const mobileRoutes: Routes = [
  {
    path: '',
    component: MobileShell,
    children: [
      { path: '', pathMatch: 'full', redirectTo: 'welcome' },
      { path: 'welcome', component: MobileWelcome },
      { path: 'home', component: MobileHome },
      { path: 'calls', component: MobileCalls },
      { path: 'units', component: MobileUnits },
      { path: 'evidence', component: MobileEvidence },
      { path: 'settings', component: MobileSettings },
      { path: '**', redirectTo: 'welcome' },
    ],
  },
];
