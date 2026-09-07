package transcription

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestProviderRequestMeasurement(t *testing.T) {
	p := provider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"text":"fixture"}`))
	}, nil)
	m := RequestMeasurement{}
	before := time.Now()
	_, err := p.Transcribe(withMeasurement(context.Background(), &m), audioFile(t, 10))
	if err != nil || m.StartedAt.Before(before) || m.Duration <= 0 || m.StartedAt.Location() != time.UTC {
		t.Fatal("missing request measurement", err, m)
	}
	m = RequestMeasurement{}
	_, _ = p.Transcribe(withMeasurement(context.Background(), &m), nil)
	if !m.StartedAt.IsZero() || m.Duration != 0 {
		t.Fatal("unissued request measured")
	}
}
