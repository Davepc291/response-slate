package transcription

import (
	"context"
	"io"
	"net/http"
	"testing"
)

type tripFunc func(*http.Request) (*http.Response, error)

func (f tripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type endlessBody struct {
	read   int
	closed bool
}

func (b *endlessBody) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	b.read += len(p)
	return len(p), nil
}
func (b *endlessBody) Close() error { b.closed = true; return nil }
func TestBoundedReadsAndEarlyResponse(t *testing.T) {
	for _, status := range []int{200, 503} {
		body := &endlessBody{}
		o := DefaultOptions()
		o.Enabled = true
		o.BaseURL = "https://speech.example.invalid"
		p, err := NewHTTPProvider(o, tripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: body, Header: make(http.Header), Request: r}, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		// Transport returns before consuming the upload; the multipart producer must
		// still terminate when the response is rejected and the pipe is closed.
		_, err = p.Transcribe(context.Background(), audioFile(t, 4096))
		p.Close()
		limit := int(o.MaxResponseBytes) + 1
		if status != 200 {
			limit = 4097
		}
		if err == nil || body.read != limit || !body.closed {
			t.Fatal("body not bounded/closed")
		}
	}
}
func TestCanceledBeforeUpload(t *testing.T) {
	p := provider(t, func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body) }, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Transcribe(ctx, audioFile(t, 4096))
	if err == nil || SafeFailure(err).Code != "canceled" {
		t.Fatal("canceled upload accepted")
	}
}
