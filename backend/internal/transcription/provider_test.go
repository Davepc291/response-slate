package transcription

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func audioFile(t *testing.T, n int) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.mp3")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", n)), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
func provider(t *testing.T, h http.HandlerFunc, adjust func(*Options)) *HTTPProvider {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	o := DefaultOptions()
	o.Enabled = true
	o.BaseURL = s.URL
	if adjust != nil {
		adjust(&o)
	}
	p, err := NewHTTPProvider(o, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}
func TestStreamingMultipart(t *testing.T) {
	p := provider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("request contract")
		}
		if r.ContentLength != -1 {
			t.Error("upload should stream with unknown multipart length")
		}
		mr, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			return
		}
		fields := map[string]string{}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Error(err)
				return
			}
			b, err := io.ReadAll(part)
			if err != nil {
				t.Error(err)
			}
			fields[part.FormName()] = string(b)
		}
		for k, v := range map[string]string{"file": strings.Repeat("a", 4096), "model": "small.en", "language": "en", "prompt": "", "response_format": "json", "temperature": "0", "beam_size": "5", "best_of": "5", "word_boost": ""} {
			got, ok := fields[k]
			if !ok || got != v {
				t.Errorf("field %s", k)
			}
		}
		io.WriteString(w, `{"text":"  Thank you  for reporting on the distraction.\n"}`)
	}, func(o *Options) { o.BearerToken = "fixture-token" })
	r, err := p.Transcribe(context.Background(), audioFile(t, 4096))
	if err != nil {
		t.Fatal(err)
	}
	if r.RawText != "  Thank you  for reporting on the distraction.\n" || r.Text != "Thank you for reporting on the distraction." {
		t.Fatal("provider words or raw evidence changed")
	}
}
func TestResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		code       string
		retry      bool
	}{
		{"malformed", "{", 200, "invalid_response", false}, {"trailing", `{"text":"ok"}{}`, 200, "invalid_response", false},
		{"missing", `{}`, 200, "invalid_response", false}, {"null", `{"text":null}`, 200, "invalid_response", false},
		{"numeric", `{"text":12}`, 200, "invalid_response", false}, {"empty", `{"text":" \n"}`, 200, "invalid_response", false},
		{"invalid Unicode", `{"text":"\ud800"}`, 200, "invalid_response", false},
		{"control", `{"text":"bad\u0000"}`, 200, "invalid_response", false}, {"bidi", `{"text":"bad\u202e"}`, 200, "invalid_response", false},
		{"oversized", `{"text":"` + strings.Repeat("a", 1200) + `"}`, 200, "invalid_response", false},
		{"auth", strings.Repeat("fixture-secret", 1000), 401, "provider_rejected", false},
		{"rate", "fixture-secret", 429, "provider_rejected", true}, {"server", "fixture-secret", 503, "provider_rejected", true},
		{"other success", `{"text":"ok"}`, 201, "provider_rejected", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := provider(t, func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}, func(o *Options) { o.MaxResponseBytes = 1024 })
			_, err := p.Transcribe(context.Background(), audioFile(t, 10))
			if err == nil {
				t.Fatal("accepted invalid response")
			}
			f := SafeFailure(err)
			if f.Code != tc.code || f.Retryable != tc.retry || strings.Contains(err.Error(), "secret") {
				t.Fatal("unsafe or incorrect failure")
			}
		})
	}
}
func TestNoRedirect(t *testing.T) {
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer target.Close()
	p := provider(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		http.Redirect(w, r, target.URL, 307)
	}, nil)
	_, err := p.Transcribe(context.Background(), audioFile(t, 10))
	if err == nil || reached {
		t.Fatal("redirect followed")
	}
}
func TestInputLimit(t *testing.T) {
	reached := false
	p := provider(t, func(w http.ResponseWriter, r *http.Request) { reached = true }, func(o *Options) { o.MaxAudioBytes = 1024 })
	_, err := p.Transcribe(context.Background(), audioFile(t, 1025))
	if err == nil || SafeFailure(err).Code != "audio_too_large" || reached {
		t.Fatal("input limit not enforced before upload")
	}
}
func TestTimeoutAndCancellation(t *testing.T) {
	for _, cancelEarly := range []bool{false, true} {
		t.Run(map[bool]string{true: "cancel", false: "timeout"}[cancelEarly], func(t *testing.T) {
			started := make(chan struct{})
			p := provider(t, func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				close(started)
				<-r.Context().Done()
			}, func(o *Options) { o.Timeout = time.Second })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			f := audioFile(t, 4096)
			go func() { _, err := p.Transcribe(ctx, f); done <- err }()
			<-started
			if cancelEarly {
				cancel()
			}
			select {
			case err := <-done:
				want := "provider_timeout"
				if cancelEarly {
					want = "canceled"
				}
				if err == nil || SafeFailure(err).Code != want {
					t.Fatal("wrong cancellation outcome")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("request did not stop")
			}
		})
	}
}
func TestOptions(t *testing.T) {
	o := DefaultOptions()
	if o.Enabled || o.Model != "small.en" || o.Language != "en" || o.Validate() != nil {
		t.Fatal("defaults")
	}
	for _, url := range []string{"", "ftp://example.invalid", "http://user:password@example.invalid", "http://example.invalid?q=1", "http://example.invalid?", "http://example.invalid#x", "http://example.invalid#", "http://", "http://bad port", "http://example.invalid:bad"} {
		o := DefaultOptions()
		o.Enabled = true
		o.BaseURL = url
		if o.Validate() == nil {
			t.Errorf("invalid URL accepted: %q", url)
		}
	}
	if o.Backoff(100) != time.Minute || o.Backoff(1) != 5*time.Second {
		t.Fatal("backoff not bounded")
	}
	if SafeFailure(io.ErrUnexpectedEOF).Error() != "provider_unavailable" {
		t.Fatal("unsafe fallback")
	}
}
