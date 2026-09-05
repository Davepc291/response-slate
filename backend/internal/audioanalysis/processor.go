package audioanalysis

import (
	"context"
	"io"
	"log/slog"
	"os"
	"time"

	"greenwich-fire-responder/backend/internal/recordings"
)

type Store interface {
	ClaimAudio(context.Context, string, int) (bool, error)
	CompleteAudio(context.Context, string, Result) (string, error)
	FailAudio(context.Context, string, string, bool) error
}

type AudioAnalyzer interface {
	Analyze(context.Context, io.ReadSeeker) (Result, error)
}

type Processor struct {
	Options  Options
	Analyzer AudioAnalyzer
	Store    Store
	Logger   *slog.Logger
}

// Process is synchronous and bounded. The watcher remains the owner of input.
func (p *Processor) Process(ctx context.Context, r recordings.Recording, input *os.File) {
	log := func(outcome, reason string) {
		p.Logger.Info("audio_analysis", "recording_id", r.SourceIdentity, "outcome", outcome, "reason", reason)
	}
	for attempt := 0; attempt < p.Options.MaxAttempts && ctx.Err() == nil; attempt++ {
		claimed, err := p.Store.ClaimAudio(ctx, r.SourceIdentity, p.Options.MaxAttempts)
		if err != nil {
			log("failed", "database_write_failed")
		} else if !claimed {
			log("ignored", "already_processed_or_claimed")
			return
		} else {
			var result Result
			if !unchanged(input, r) {
				err = errSource
			} else {
				result, err = p.Analyzer.Analyze(ctx, input)
			}
			if err == nil && !unchanged(input, r) {
				err = errSource
			}
			if err == nil {
				var outcome string
				outcome, err = p.Store.CompleteAudio(ctx, r.SourceIdentity, result)
				if err == nil {
					switch outcome {
					case "analyzed", "duplicate-content", "already-processed":
						log(outcome, "audio_analysis_complete")
					default:
						log("failed", "unexpected_database_outcome")
					}
					return
				}
			}
			failure := safeFailure(err)
			retry := failure.retryable && attempt+1 < p.Options.MaxAttempts
			// Persist cancellation with a short independent deadline before pool close.
			persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			persistErr := p.Store.FailAudio(persistCtx, r.SourceIdentity, failure.code, retry)
			cancel()
			if persistErr != nil {
				log("failed", "database_write_failed")
			}
			if failure.retryable {
				log("failed", failure.code)
			} else {
				log("invalid", failure.code)
			}
			if !retry || persistErr != nil {
				return
			}
		}
		if attempt+1 < p.Options.MaxAttempts {
			timer := time.NewTimer(p.Options.RetryInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
}

func unchanged(input *os.File, r recordings.Recording) bool {
	if input == nil {
		return false
	}
	info, err := input.Stat()
	return err == nil && info.Mode().IsRegular() && info.Size() == r.SizeBytes && info.ModTime().Equal(r.ModifiedAt)
}
