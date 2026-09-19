import { Component } from '@angular/core';

import {
  ACTIVE_INCIDENT,
  AMBIGUOUS_REJECTED,
  CHANNELS,
  EVIDENCE_ONLY_NOTE,
  REPLAY_EVIDENCE,
} from '../../board-preview/board-preview.fixtures';

@Component({
  selector: 'app-mobile-evidence',
  templateUrl: './mobile-evidence.html',
  styleUrl: './mobile-evidence.scss',
})
export class MobileEvidence {
  readonly channels = CHANNELS;
  readonly incident = ACTIVE_INCIDENT;
  readonly evidenceOnlyNote = EVIDENCE_ONLY_NOTE;
  readonly replay = REPLAY_EVIDENCE;
  readonly ambiguousRejected = AMBIGUOUS_REJECTED;
}
