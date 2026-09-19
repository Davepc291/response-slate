import { Routes } from '@angular/router';

import { BoardPreview } from './board-preview/board-preview';
import { mobileRoutes } from './mobile/mobile.routes';

export const routes: Routes = [
  { path: '', component: BoardPreview, pathMatch: 'full' },
  { path: 'mobile', children: mobileRoutes },
];
