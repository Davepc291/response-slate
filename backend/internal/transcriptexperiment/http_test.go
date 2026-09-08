package transcriptexperiment

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func TestFixedLocalMultipart(t *testing.T) {
	for _, prompt := range []string{"", "SYNTHETIC prompt"} {
		calls := 0
		p := newProvider(time.Second, transportFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.String() != Endpoint || r.Method != "POST" || r.Header.Get("Authorization") != "" {
				t.Fatal("unsafe endpoint")
			}
			if e := r.ParseMultipartForm(1 << 20); e != nil {
				t.Fatal(e)
			}
			defer r.MultipartForm.RemoveAll()
			for key, want := range map[string]string{"model": "small.en", "language": "en", "response_format": "json", "temperature": "0", "beam_size": "5", "best_of": "5", "word_boost": "", "prompt": prompt} {
				if r.FormValue(key) != want {
					t.Fatal("multipart mismatch", key)
				}
			}
			if prompt == "" {
				if _, ok := r.MultipartForm.Value["prompt"]; ok {
					t.Fatal("baseline prompt sent")
				}
			}
			f, h, e := r.FormFile("file")
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			b, _ := io.ReadAll(f)
			if string(b) != "SYNTHETIC AUDIO" || h.Filename != "recording.mp3" {
				t.Fatal("upload")
			}
			return response(200, `{"text":"  SYNTHETIC result\n"}`), nil
		}))
		raw, out, e := p.Send(context.Background(), []byte("SYNTHETIC AUDIO"), ".mp3", prompt)
		p.Close()
		if e != nil || raw != "  SYNTHETIC result\n" || calls != 1 || out.HTTPStatus != 200 || out.DurationMS < 0 {
			t.Fatal(e, out)
		}
	}
	p, e := NewProvider(time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	tr := p.(*provider).client.Transport.(*http.Transport)
	if tr.Proxy != nil || !tr.DisableKeepAlives {
		t.Fatal("proxy or retries enabled")
	}
}
func TestNoRedirectNoRetryAndBadResponses(t *testing.T) {
	cases := []struct {
		status int
		body   string
	}{{302, ""}, {500, "SYNTHETIC SECRET"}, {200, `{}`}, {200, `{"text":null}`}, {200, `{"text":"a","text":"b"}`}, {200, "\xff"}, {200, `{"text":"\ud800"}`}, {200, `{"text":"\u0000"}`}, {200, strings.Repeat("x", MaxResponseBytes+1)}, {200, `{"text":"x"} {}`}}
	for _, c := range cases {
		calls := 0
		p := newProvider(time.Second, transportFunc(func(*http.Request) (*http.Response, error) {
			calls++
			r := response(c.status, c.body)
			r.Header.Set("Location", "http://example.invalid/")
			return r, nil
		}))
		_, out, e := p.Send(context.Background(), []byte("synthetic"), ".mp3", "")
		p.Close()
		if e == nil || calls != 1 || out.HTTPStatus != c.status || strings.Contains(e.Error(), "SECRET") {
			t.Fatal("unsafe request response", e)
		}
	}
	p := newProvider(time.Second, transportFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() }))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, e := p.Send(ctx, []byte("synthetic"), ".wav", ""); e != ErrRequest {
		t.Fatal("cancellation")
	}
	p = newProvider(5*time.Millisecond, transportFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() }))
	if _, _, e := p.Send(context.Background(), []byte("synthetic"), ".wav", ""); e != ErrRequest {
		t.Fatal("timeout")
	}
}
