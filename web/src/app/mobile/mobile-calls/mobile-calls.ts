import { Component } from '@angular/core';

import {
  ACTIVE_INCIDENT,
  EVIDENCE_ONLY_NOTE,
  PENDING,
} from '../../board-preview/board-preview.fixtures';

@Component({
  selector: 'app-mobile-calls',
  templateUrl: './mobile-calls.html',
  styleUrl: './mobile-calls.scss',
})
export class MobileCalls {
  readonly pending = PENDING;
  readonly incident = ACTIVE_INCIDENT;
  readonly evidenceOnlyNote = EVIDENCE_ONLY_NOTE;
}
