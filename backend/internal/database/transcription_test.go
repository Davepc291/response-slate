package database

import (
	"context"
	"errors"
	"greenwich-fire-responder/backend/internal/transcription"
	"strings"
	"testing"
	"time"
)

func TestTranscriptionDatabaseBoundary(t *testing.T) {
	p := &audioPool{}
	db := &DB{pool: p}
	ctx := context.Background()
	_, _, err := db.NextTranscription(ctx, t.TempDir(), transcription.DefaultOptions())
	if err != nil || !p.deadline || !strings.Contains(p.sql, "claim_transcription") {
		t.Fatal("claim boundary")
	}
	j := transcription.Job{ID: 1, Claim: transcription.NewClaim()}
	_, err = db.FinishTranscription(ctx, j, transcription.Result{RawText: "raw", Text: "raw"}, nil, time.Second)
	if err != nil || !p.deadline || !strings.Contains(p.sql, "finish_transcription") || p.args[2] != "raw" || p.args[3] != "raw" {
		t.Fatal("atomic persistence boundary")
	}
	_, err = db.FinishTranscription(ctx, j, transcription.Result{}, &transcription.Failure{Code: "fixture-secret"}, time.Second)
	if err != nil || p.args[4] != "provider_unavailable" {
		t.Fatal("unsafe failure persisted")
	}
	p.row.err = errors.New("fixture-secret")
	if _, _, err = db.NextTranscription(ctx, t.TempDir(), transcription.DefaultOptions()); err != ErrUnavailable {
		t.Fatal("unsafe driver error")
	}
	if _, err = db.FinishTranscription(ctx, j, transcription.Result{}, nil, time.Second); err != ErrUnavailable {
		t.Fatal("unsafe finish error")
	}
	if _, _, err = (&DB{}).NextTranscription(ctx, t.TempDir(), transcription.DefaultOptions()); err != ErrUnavailable {
		t.Fatal("unconfigured database")
	}
}
