import { DatePipe } from '@angular/common';
import { Component, DestroyRef, inject, signal } from '@angular/core';

import { PwaStatus } from '../pwa-status/pwa-status';
import {
  ACTIVE_INCIDENT,
  AMBIGUOUS_REJECTED,
  CHANNELS,
  EVIDENCE_ONLY_NOTE,
  HEADER_TITLE,
  OUT_OF_SERVICE,
  PENDING,
  PROPOSED_STATUS_NOTE,
  REPLAY_EVIDENCE,
  ROSTER,
  SHADOW_WARNING,
  SYNTHETIC_WARNING,
} from './board-preview.fixtures';

@Component({
  selector: 'app-board-preview',
  imports: [DatePipe, PwaStatus],
  templateUrl: './board-preview.html',
  styleUrl: './board-preview.scss',
})
export class BoardPreview {
  readonly shadowWarning = SHADOW_WARNING;
  readonly syntheticWarning = SYNTHETIC_WARNING;
  readonly headerTitle = HEADER_TITLE;
  readonly proposedNote = PROPOSED_STATUS_NOTE;
  readonly evidenceOnlyNote = EVIDENCE_ONLY_NOTE;
  readonly channels = CHANNELS;
  readonly roster = ROSTER;
  readonly pending = PENDING;
  readonly incident = ACTIVE_INCIDENT;
  readonly replay = REPLAY_EVIDENCE;
  readonly ambiguousRejected = AMBIGUOUS_REJECTED;
  readonly outOfService = OUT_OF_SERVICE;
  readonly now = signal(new Date());
  private readonly destroyRef = inject(DestroyRef);

  constructor() {
    const id = window.setInterval(() => this.now.set(new Date()), 1000);
    this.destroyRef.onDestroy(() => window.clearInterval(id));
  }
}
