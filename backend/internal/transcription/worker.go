package transcription

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"greenwich-fire-responder/backend/internal/operations"
	"greenwich-fire-responder/backend/internal/recordings"
	"log/slog"
	"time"
)

type Job struct {
	ID          int64
	Claim       string
	Attempt     int
	Recording   recordings.Recording
	Measurement RequestMeasurement
}
type Store interface {
	NextTranscription(context.Context, string, Options) (Job, bool, error)
	FinishTranscription(context.Context, Job, Result, *Failure, time.Duration) (bool, error)
}
type Worker struct {
	Options   Options
	Directory string
	Store     Store
	Provider  Provider
	Logger    *slog.Logger
	Monitor   *operations.Monitor
}

func NewClaim() string { var b [16]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }

func (w *Worker) Run(ctx context.Context) {
	if !w.Options.Enabled || w.Directory == "" {
		w.Logger.Info("transcription", "outcome", "disabled")
		return
	}
	defer w.Monitor.Activity("stopped")
	timer := time.NewTimer(0)
	defer timer.Stop()
	databaseFailed := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		w.Monitor.Activity("claiming")
		job, found, err := w.Store.NextTranscription(ctx, w.Directory, w.Options)
		if err != nil {
			w.Monitor.Activity("database_unavailable")
			// Emit once per failure episode, not once per polling tick.
			if !databaseFailed {
				w.Logger.Warn("transcription", "outcome", "failed", "reason", "database_unavailable")
			}
			databaseFailed = true
		} else {
			databaseFailed = false
			w.Monitor.Activity("idle")
		}
		if err == nil && found {
			w.process(ctx, job)
		}
		timer.Reset(time.Second)
	}
}
func (w *Worker) process(ctx context.Context, j Job) {
	w.Monitor.Activity("preparing")
	defer w.Monitor.Activity("idle")
	result := Result{}
	var err error
	f, err := recordings.OpenSource(w.Directory, j.Recording)
	if err != nil {
		err = Failure{"source_unavailable", false}
	} else {
		w.Logger.Info("transcription", "transmission_id", j.ID, "attempt", j.Attempt, "outcome", "request_started")
		w.Monitor.Activity("requesting")
		result, err = w.Provider.Transcribe(withMeasurement(ctx, &j.Measurement), f)
		info, statErr := f.Stat()
		f.Close()
		if err == nil && (statErr != nil || info.Size() != j.Recording.SizeBytes || !recordings.SameStoredTime(info.ModTime(), j.Recording.ModifiedAt)) {
			err = Failure{"source_unavailable", false}
		}
	}
	var failure *Failure
	outcome := "completed"
	if err != nil {
		v := SafeFailure(err)
		v.Retryable = v.Retryable && j.Attempt < w.Options.MaxAttempts
		failure = &v
		outcome = "permanent_failure"
		if v.Retryable {
			outcome = "retry"
		}
		if v.Code == "canceled" {
			outcome = "canceled"
		}
		result = Result{}
	}
	code := ""
	retry := false
	if failure != nil {
		code = failure.Code
		retry = failure.Retryable
	}
	w.Monitor.ProviderResult(code, j.Measurement.Duration, !j.Measurement.StartedAt.IsZero(), retry)
	w.Monitor.Activity("persisting")
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	saved, err := w.Store.FinishTranscription(persist, j, result, failure, w.Options.Backoff(j.Attempt))
	if err != nil || !saved {
		w.Logger.Warn("transcription", "transmission_id", j.ID, "outcome", "failed", "reason", "persistence_unavailable_or_stale_claim")
		return
	}
	reason := "transcript_stored"
	if failure != nil {
		reason = failure.Code
	}
	w.Logger.Info("transcription", "transmission_id", j.ID, "outcome", outcome, "reason", reason)
}
