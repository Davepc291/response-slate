BEGIN;

-- Step 8D-B Part 10: fenced claim/lease foundation for notification_outbox
-- (docs/notification-relay-amendment-v1.md Section 10). Adds exactly one
-- nullable column and one additive CHECK constraint; it creates no new
-- table, drops nothing, and does not alter any existing column, index,
-- trigger, or constraint from migration 000010. Every one of that
-- migration's existing protections is preserved unchanged: the
-- matched-event-only gate (notification_outbox_requires_matched_event),
-- the (event_id, device_id) idempotency key, the composite ownership
-- foreign key to notification_devices, the attempt-count bound
-- (notification_outbox_attempt_count_bounded), and every outbox
-- terminal-state protection already enforced by the existing
-- "WHERE state = 'pending'" discipline in backend/internal/notifyoutboxstore.
--
-- claim_token is a fencing token for the currently-held delivery-attempt
-- claim, if any: NULL means unclaimed. This directly mirrors the existing,
-- already-reviewed radio_transmissions.transcription_claim / uuid pattern
-- (migration 000004_transcription.sql) -- the same "claim + lease +
-- fencing" shape, applied here to notification_outbox instead of
-- radio_transmissions.
--
-- next_attempt_at is deliberately reused as the dual-purpose retry-due-time
-- (claim_token IS NULL) / lease-expiry (claim_token IS NOT NULL) column,
-- exactly mirroring radio_transmissions.transcription_next_at's own
-- documented dual meaning ("Retry due time or processing lease expiry.").
-- No second timestamp column is added: "this row is actionable right now"
-- is always exactly `state = 'pending' AND next_attempt_at <= now`,
-- regardless of which of the two meanings currently applies, so the
-- existing notification_outbox_pending_idx (next_attempt_at) WHERE
-- state = 'pending' already fully serves both the original retry-due
-- query shape and this migration's new claim-scan query shape -- no new
-- index is added or needed.
ALTER TABLE notification_outbox
    ADD COLUMN claim_token uuid,
    ADD CONSTRAINT notification_outbox_claim_implies_pending
        CHECK (claim_token IS NULL OR state = 'pending');

COMMENT ON COLUMN notification_outbox.claim_token IS
    'Fencing token for the currently-held delivery-attempt claim, if any. NULL means unclaimed. Set by ClaimNextDue/ClaimSpecific, checked by MarkSent/RecordFailedAttempt/ReleaseClaim to reject a stale completion from a worker whose lease has already been reclaimed by a different worker, cleared by every one of those methods on completion or release. Cancel/MarkExpired may only operate when this column is NULL: an active claim is never silently overridden or cleared by an unrelated terminal transition. Reuses next_attempt_at as the dual-purpose retry-due-time/lease-expiry column, mirroring radio_transmissions.transcription_claim/transcription_next_at (migration 000004_transcription.sql).';

INSERT INTO schema_migrations (version) VALUES ('000011');

COMMIT;
