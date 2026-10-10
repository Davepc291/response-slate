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
// Real eligibility evaluation (notifypreferences.Evaluator wired to
// identitystore/notifydevicestore/notifyconsentstore/notifyprefsstore) is
// explicitly NOT exercised here: this package's own test uses a fixed,
// always-eligible fake evaluator instead, exactly mirroring every existing
// notifyworker test (backend/internal/notifyworker/notifyworker_test.go's own
// fakeEvaluator). Consent, preferences, and quiet-hours therefore remain
// unproven end-to-end after this part -- see the test's own doc comment for
// the full list of what is, and is not, proven.
package shadowdeliverytest
