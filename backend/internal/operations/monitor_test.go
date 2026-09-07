package operations

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type sourceFunc func(context.Context, string, int) (Metrics, error)

func (f sourceFunc) TranscriptionMetrics(c context.Context, d string, a int) (Metrics, error) {
	return f(c, d, a)
}
func TestStatesBoundariesAndPrivacy(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	age := 30000.0
	data := Metrics{ObservedAt: now}
	var sourceErr error
	var logs bytes.Buffer
	m := New(sourceFunc(func(context.Context, string, int) (Metrics, error) { return data, sourceErr }), "private/path", 3, true, DefaultOptions(), slog.New(slog.NewJSONHandler(&logs, nil)))
	m.Now = func() time.Time { return now }
	if s := m.Snapshot(context.Background()); s.Status != "provider_unknown" || s.Metrics.Waiting != 0 || s.Metrics.OldestAgeMS != nil {
		t.Fatal(s)
	}
	m.ProviderResult("", time.Second, true, false)
	if s := m.Snapshot(context.Background()); s.Status != "healthy_idle" {
		t.Fatal(s)
	}
	data.Waiting = 9
	data.OldestAgeMS = &age
	age = 29999
	m.Snapshot(context.Background())
	if strings.Contains(logs.String(), "backlog") || strings.Contains(logs.String(), "oldest_job_age") {
		t.Fatal(logs.String())
	}
	data.Waiting = 10
	age = 30000
	data.Processing = 1
	if s := m.Snapshot(context.Background()); s.Status != "healthy_backlog" || s.Metrics.Processing != 1 {
		t.Fatal(s)
	}
	for i := 0; i < 100; i++ {
		m.Snapshot(context.Background())
	}
	if strings.Count(logs.String(), `"event":"backlog"`) != 1 || strings.Count(logs.String(), `"event":"oldest_job_age"`) != 1 {
		t.Fatal(logs.String())
	}
	for i := 0; i < 4; i++ {
		m.ProviderResult("provider_timeout", 15*time.Second, true, true)
	}
	s := m.Snapshot(context.Background())
	if s.Status != "provider_unavailable" || s.ConsecutiveFailures != 4 {
		t.Fatal(s)
	}
	if strings.Count(logs.String(), `"event":"retry"`) != 1 || strings.Count(logs.String(), `"event":"provider_timeout"`) != 1 || strings.Count(logs.String(), `"event":"provider_failures"`) != 1 {
		t.Fatal(logs.String())
	}
	m.ProviderResult("", time.Second, true, false)
	m.ProviderResult("", time.Second, true, false)
	if strings.Count(logs.String(), `"event":"provider_recovery"`) != 1 {
		t.Fatal(logs.String())
	}
	now = now.Add(61 * time.Second)
	if s = m.Snapshot(context.Background()); s.Provider != "stale" || s.Status != "provider_unknown" {
		t.Fatal(s)
	}
	m.Activity("disabled")
	if s = m.Snapshot(context.Background()); s.Status != "worker_disabled" {
		t.Fatal(s)
	}
	sourceErr = errors.New("secret transcript filename RID https://provider.invalid/path")
	if s = m.Snapshot(context.Background()); s.Status != "database_unavailable" || s.Metrics != nil {
		t.Fatal(s)
	}
	if strings.Contains(logs.String(), "secret") || strings.Contains(logs.String(), "private/path") {
		t.Fatal("unsafe logs")
	}
}
func TestMonitorCancellation(t *testing.T) {
	called := make(chan struct{})
	m := New(sourceFunc(func(c context.Context, _ string, _ int) (Metrics, error) {
		close(called)
		<-c.Done()
		return Metrics{}, c.Err()
	}), "", 3, false, DefaultOptions(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	<-called
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("monitor did not stop")
	}
}
func TestWarningValidation(t *testing.T) {
	if DefaultOptions().Validate() != nil {
		t.Fatal("defaults")
	}
	for _, o := range []Options{{0, time.Second, time.Second, 3}, {1, 0, time.Second, 3}, {1, time.Second, 0, 3}, {1, time.Second, time.Second, 0}} {
		if o.Validate() == nil {
			t.Fatal("invalid accepted")
		}
	}
}

func TestRequestWarningBoundaries(t *testing.T) {
	var logs bytes.Buffer
	m := New(nil, "", 3, false, DefaultOptions(), slog.New(slog.NewJSONHandler(&logs, nil)))
	m.ProviderResult("", 15*time.Second-time.Nanosecond, true, false)
	if logs.Len() != 0 {
		t.Fatal("warning below duration threshold")
	}
	m.ProviderResult("provider_unavailable", 15*time.Second, true, true)
	m.ProviderResult("provider_unavailable", 15*time.Second, true, true)
	if strings.Contains(logs.String(), `"event":"provider_failures"`) {
		t.Fatal("warning below failure threshold")
	}
	m.ProviderResult("provider_unavailable", 15*time.Second, true, true)
	if strings.Count(logs.String(), `"event":"slow_provider_request"`) != 1 || strings.Count(logs.String(), `"event":"provider_failures"`) != 1 {
		t.Fatal(logs.String())
	}
}
