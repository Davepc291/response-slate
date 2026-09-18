export const SHADOW_WARNING = 'SHADOW / REPLAY — NOT LIVE CAD';
export const SYNTHETIC_WARNING = 'Synthetic replay preview. No operational authority.';
export const HEADER_TITLE = 'Greenwich Fire Responder V3';
export const PROPOSED_STATUS_NOTE = 'Proposed association — not current apparatus state.';
export const EVIDENCE_ONLY_NOTE =
  'Channel and TGID are evidence only. They do not authorize incidents or unit state.';
export const NOT_ONE_INCIDENT_NOTE =
  'Address resolved independently. Call type unresolved. This is not proof of one incident.';

export const CHANNELS = ['CH1A', 'CH2B', 'CH3B', 'CH4C'] as const;

export type ChannelLabel = (typeof CHANNELS)[number];

export type UnitId = 'DC' | 'E2' | 'E3' | 'E4' | 'E5' | 'SQ1' | 'SQ8' | 'T1';

export type UnitStatusKind =
  'quarters' | 'available' | 'enroute' | 'onscene' | 'enroute_to_quarters' | 'on_air' | 'training';

export interface RosterUnit {
  readonly id: UnitId;
  readonly station: string;
  readonly statusKind: UnitStatusKind;
  readonly statusLabel: string;
  readonly proposed: boolean;
}

export interface PendingDispatch {
  readonly reference: string;
  readonly address: string;
  readonly callType: string;
  readonly channel: ChannelLabel;
  readonly tgid: string;
  readonly note: string;
}

export interface ActiveIncident {
  readonly reference: string;
  readonly address: string;
  readonly addressState: string;
  readonly callType: string;
  readonly callTypeState: string;
  readonly channel: ChannelLabel;
  readonly tgid: string;
  readonly note: string;
}

export interface ReplayEvidence {
  readonly shadowOnly: boolean;
  readonly pairingIntact: boolean;
  readonly rawModel: string;
  readonly humanReference: string;
  readonly difference: string;
}

export interface AmbiguousRejectedItem {
  readonly kind: 'ambiguous' | 'rejected' | 'unbound';
  readonly label: string;
  readonly detail: string;
}

export interface OutOfServicePreview {
  readonly label: string;
  readonly note: string;
}

export const ROSTER: readonly RosterUnit[] = [
  { id: 'DC', station: 'HQ', statusKind: 'quarters', statusLabel: 'QUARTERS', proposed: false },
  { id: 'E2', station: 'STN2', statusKind: 'onscene', statusLabel: 'ONSCENE', proposed: true },
  { id: 'E3', station: 'STN3', statusKind: 'enroute', statusLabel: 'ENROUTE', proposed: true },
  { id: 'E4', station: 'STN4', statusKind: 'available', statusLabel: 'AVAILABLE', proposed: false },
  { id: 'E5', station: 'STN5', statusKind: 'on_air', statusLabel: 'ON AIR', proposed: true },
  { id: 'SQ1', station: 'HQ', statusKind: 'training', statusLabel: 'TRAINING', proposed: true },
  {
    id: 'SQ8',
    station: 'STN8',
    statusKind: 'enroute_to_quarters',
    statusLabel: 'ENROUTE TO QUARTERS',
    proposed: true,
  },
  { id: 'T1', station: 'HQ', statusKind: 'quarters', statusLabel: 'QUARTERS', proposed: false },
];

export const PENDING: PendingDispatch = {
  reference: 'SYN-PENDING-01',
  address: '100 TEST STREET (PENDING)',
  callType: 'unresolved',
  channel: 'CH1A',
  tgid: '57201',
  note: 'Pending dispatch evidence. Not an incident. Not live CAD.',
};

export const ACTIVE_INCIDENT: ActiveIncident = {
  reference: 'SYN-INCIDENT-01',
  address: '100 TEST STREET',
  addressState: 'resolved',
  callType: 'unresolved',
  callTypeState: 'unresolved',
  channel: 'CH1A',
  tgid: '57201',
  note: NOT_ONE_INCIDENT_NOTE,
};

export const REPLAY_EVIDENCE: ReplayEvidence = {
  shadowOnly: true,
  pairingIntact: true,
  rawModel: 'SYNTHETIC RAW MODEL: TEST STREET DISPATCH PHRASE',
  humanReference: 'SYNTHETIC HUMAN REFERENCE: respond to 100 TEST STREET',
  difference: 'Raw-model and human-reference fixtures differ. Neither is operational truth.',
};

export const AMBIGUOUS_REJECTED: readonly AmbiguousRejectedItem[] = [
  {
    kind: 'ambiguous',
    label: 'Ambiguous unit evidence',
    detail: 'Synthetic phrase ENGINE 2 AND ENGINE 20 remains ambiguous. Not unit state.',
  },
  {
    kind: 'rejected',
    label: 'Rejected unit evidence',
    detail: 'Synthetic ENGINE 2 ON SCENE rejected as unsupported_context. Not apparatus state.',
  },
  {
    kind: 'unbound',
    label: 'Unbound status association',
    detail: 'Synthetic ON SCENE cue with no accepted unit pointer. Proposed association only.',
  },
];

export const OUT_OF_SERVICE: OutOfServicePreview = {
  label: 'Synthetic out-of-service preview',
  note: 'Non-operational fixture. Apparatus out-of-service is not enabled and has no CAD authority.',
};
