// Package operations exposes bounded, observational transcription telemetry.
package operations

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

type Options struct {
	QueueDepth                 int64
	OldestAge, RequestDuration time.Duration
	ConsecutiveFailures        int
}

func DefaultOptions() Options { return Options{10, 30 * time.Second, 15 * time.Second, 3} }
func (o Options) Validate() error {
	if o.QueueDepth < 1 || o.QueueDepth > 100000 || o.OldestAge < time.Second || o.OldestAge > 24*time.Hour || o.RequestDuration < time.Millisecond || o.RequestDuration > 5*time.Minute || o.ConsecutiveFailures < 1 || o.ConsecutiveFailures > 100 {
		return errors.New("invalid operations warning configuration")
	}
	return nil
}

type Summary struct {
	Samples int64    `json:"samples"`
	MeanMS  *float64 `json:"mean_ms"`
	P95MS   *float64 `json:"p95_ms"`
	MaxMS   *float64 `json:"max_ms"`
}
type Timings struct {
	IngestionToClaim      Summary `json:"ingestion_to_claim"`
	ClaimToRequest        Summary `json:"claim_to_request"`
	ProviderRequest       Summary `json:"provider_request"`
	ClaimToCompletion     Summary `json:"claim_to_completion"`
	IngestionToCompletion Summary `json:"ingestion_to_completion"`
}
type Metrics struct {
	ObservedAt          time.Time  `json:"observed_at"`
	Waiting             int64      `json:"waiting_jobs"`
	OldestAgeMS         *float64   `json:"oldest_waiting_age_ms"`
	Processing          int64      `json:"processing_jobs"`
	RetryWaiting        int64      `json:"retry_waiting_jobs"`
	Completed           int64      `json:"completed_jobs"`
	Failed              int64      `json:"failed_jobs"`
	Skipped             int64      `json:"skipped_jobs"`
	Attempts            int64      `json:"attempt_count"`
	Retries             int64      `json:"retry_count"`
	Timeouts            int64      `json:"timeout_count"`
	LastSuccess         *time.Time `json:"last_success_at"`
	LastProviderFailure *time.Time `json:"last_provider_failure_at"`
	Recent              Timings    `json:"recent_timings"`
}
type Source interface {
	TranscriptionMetrics(context.Context, string, int) (Metrics, error)
}
type Snapshot struct {
	Status              string    `json:"status"`
	Database            string    `json:"database"`
	Worker              string    `json:"worker_state"`
	Provider            string    `json:"provider_state"`
	ConsecutiveFailures int       `json:"consecutive_provider_failures"`
	Metrics             *Metrics  `json:"metrics"`
	ObservedAt          time.Time `json:"observed_at"`
}
type Monitor struct {
	Source           Source
	Directory        string
	MaxAttempts      int
	Options          Options
	Logger           *slog.Logger
	Now              func() time.Time
	mu               sync.Mutex
	worker, provider string
	providerAt       time.Time
	failures         int
	warnings         map[string]bool
}

func New(source Source, directory string, attempts int, enabled bool, o Options, logger *slog.Logger) *Monitor {
	if o == (Options{}) {
		o = DefaultOptions()
	}
	worker := "disabled"
	if enabled && directory != "" {
		worker = "idle"
	}
	return &Monitor{Source: source, Directory: directory, MaxAttempts: attempts, Options: o, Logger: logger, Now: time.Now, worker: worker, provider: "unknown", warnings: map[string]bool{}}
}
func (m *Monitor) Activity(state string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.worker = state
}

// warn is called with mu held. Fixed keys log only transitions, not every poll.
func (m *Monitor) warn(key string, active bool) {
	if m.warnings[key] == active {
		return
	}
	m.warnings[key] = active
	if m.Logger != nil {
		m.Logger.Info("transcription_monitor", "event", key, "active", active)
	}
}
func (m *Monitor) ProviderResult(code string, duration time.Duration, requested bool, retry bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	providerFailure := code == "provider_unavailable" || code == "provider_timeout" || code == "provider_rejected" || code == "invalid_response"
	if requested && (code == "" || providerFailure) {
		m.providerAt = m.Now().UTC()
		if providerFailure {
			m.provider = "recent_failure"
			m.failures++
		} else {
			if m.failures > 0 && m.Logger != nil {
				m.Logger.Info("transcription_monitor", "event", "provider_recovery")
			}
			m.provider = "recent_success"
			m.failures = 0
		}
	}
	m.warn("slow_provider_request", requested && duration >= m.Options.RequestDuration)
	m.warn("provider_timeout", code == "provider_timeout")
	m.warn("retry", retry)
	m.warn("provider_failures", m.failures >= m.Options.ConsecutiveFailures)
}
func (m *Monitor) Snapshot(ctx context.Context) Snapshot {
	now := m.Now().UTC()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var data Metrics
	var err error
	if m.Source == nil {
		err = errors.New("unavailable")
	} else {
		data, err = m.Source.TranscriptionMetrics(ctx, m.Directory, m.MaxAttempts)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	provider := m.provider
	if !m.providerAt.IsZero() && now.Sub(m.providerAt) > 60*time.Second {
		provider = "stale"
	}
	s := Snapshot{Status: "provider_unknown", Database: "ok", Worker: m.worker, Provider: provider, ConsecutiveFailures: m.failures, ObservedAt: now}
	if err != nil {
		s.Status = "database_unavailable"
		s.Database = "unavailable"
		m.warn("metrics_unavailable", true)
		return s
	}
	s.Metrics = &data
	m.warn("metrics_unavailable", false)
	m.warn("backlog", data.Waiting >= m.Options.QueueDepth)
	m.warn("oldest_job_age", data.OldestAgeMS != nil && *data.OldestAgeMS >= float64(m.Options.OldestAge.Milliseconds()))
	switch {
	case m.worker == "disabled":
		s.Status = "worker_disabled"
	case provider == "recent_failure":
		s.Status = "provider_unavailable"
	case provider == "recent_success":
		s.Status = "healthy_idle"
		if data.Waiting > 0 || data.Processing > 0 {
			s.Status = "healthy_backlog"
		}
	}
	return s
}
func (m *Monitor) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			m.Snapshot(ctx)
			timer.Reset(5 * time.Second)
		}
	}
}
