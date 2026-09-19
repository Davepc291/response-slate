import { Component } from '@angular/core';

import { PROPOSED_STATUS_NOTE, ROSTER } from '../../board-preview/board-preview.fixtures';

@Component({
  selector: 'app-mobile-units',
  templateUrl: './mobile-units.html',
  styleUrl: './mobile-units.scss',
})
export class MobileUnits {
  readonly roster = ROSTER;
  readonly proposedStatusNote = PROPOSED_STATUS_NOTE;
}
