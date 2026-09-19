import { Component } from '@angular/core';
import { RouterLink } from '@angular/router';

import { ACTIVE_INCIDENT, PENDING } from '../../board-preview/board-preview.fixtures';

@Component({
  selector: 'app-mobile-home',
  imports: [RouterLink],
  templateUrl: './mobile-home.html',
  styleUrl: './mobile-home.scss',
})
export class MobileHome {
  readonly pending = PENDING;
  readonly incident = ACTIVE_INCIDENT;
}
