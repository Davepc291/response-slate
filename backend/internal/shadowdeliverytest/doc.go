// Package shadowdeliverytest is the Step 8D-C Part 2 test-only proof that a
// synthetic matched detection flows through the real
// alertpipeline -> alertstore -> alertnotify -> notification_outbox ->
// notifyworker -> notifyrelay.FakeSender chain, against the real PostgreSQL
// schema, inside one rollback-only transaction.
//
// This package declares no exported symbol, is never imported by any other
// package, and is never registered by backend/cmd/api. It exists purely so
// its own live, opt-in-only integration test can import every package on
// both sides of the repository's existing reverse-dependency guards:
// internal/alerts, internal/alertstore, internal/alertpipeline, and
// internal/alertnotify all forbid importing internal/notifyworker (and vice
// versa), so no existing package directory can host a test that imports
// both sides at once -- a depcheck_test.go scans every .go file in its own
// directory regardless of declared package name, so even an external
// ("_test"-suffixed) test package placed inside one of those directories
// would still be scanned and would still fail that directory's own guard.
//
// Part 2 (integration_test.go) exercises this chain with a fixed,
// always-eligible fake evaluator instead of real eligibility evaluation,
// exactly mirroring every existing notifyworker test (backend/internal/
// notifyworker/notifyworker_test.go's own fakeEvaluator) -- see that file's
// own doc comment for exactly what it does, and does not, prove.
//
// Step 8D-C Part 3 (eligibility_integration_test.go) closes that gap: a
// real notifypreferences.Evaluator, wired to the real identitystore/
// notifydevicestore/notifyconsentstore/notifyprefsstore stores, is
// constructed and exercised both directly (every Reason value, against
// real persisted rows) and plugged into a real notifyworker.Worker
// (eligible -> send, consent-revoked -> cancel, quiet-hours -> defer).
// Consent, preferences, and quiet-hours are therefore proven end-to-end by
// that file -- but one gap remains, deliberately not closed by either part:
// nothing re-checks eligibility between Evaluate returning Eligible=true
// and the actual Sender.Send call that follows it. A consent revocation or
// preference change landing in that window does not stop the send, and a
// successful first-attempt send has no later retry to re-evaluate against.
// See eligibility_integration_test.go's own top doc comment for the full
// account of what is, and is not, proven, and why that residual race is a
// real production property, not a test gap this package can close.
package shadowdeliverytest
