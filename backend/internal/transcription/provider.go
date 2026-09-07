package transcription

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const ProviderID = "openai-compatible-http-v1"

type Result struct{ RawText, Text string }
type Provider interface {
	Transcribe(context.Context, *os.File) (Result, error)
}
type HTTPProvider struct {
	options Options
	client  *http.Client
}

// The provider owns its client; a supplied transport allows controlled tests.
func NewHTTPProvider(o Options, transport http.RoundTripper) (*HTTPProvider, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	if !o.Enabled {
		return nil, errors.New("transcription disabled")
	}
	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	return &HTTPProvider{o, &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (p *HTTPProvider) Close() { p.client.CloseIdleConnections() }

func (p *HTTPProvider) Transcribe(parent context.Context, f *os.File) (Result, error) {
	fail := func(code string, retry bool) (Result, error) { return Result{}, Failure{code, retry} }
	if f == nil {
		return fail("source_unavailable", false)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return fail("source_unavailable", false)
	}
	if info.Size() > p.options.MaxAudioBytes {
		return fail("audio_too_large", false)
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return fail("source_unavailable", false)
	}
	ctx, cancel := context.WithTimeout(parent, p.options.Timeout)
	defer cancel()
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.options.BaseURL, "/")+"/v1/audio/transcriptions", reader)
	if err != nil {
		reader.Close()
		writer.Close()
		return fail("provider_rejected", false)
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	if p.options.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.options.BearerToken)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := func() error {
			for _, field := range [][2]string{{"model", p.options.Model}, {"language", p.options.Language}, {"prompt", p.options.Prompt}, {"response_format", "json"}, {"temperature", "0"}, {"beam_size", "5"}, {"best_of", "5"}, {"word_boost", p.options.WordBoost}} {
				if err := form.WriteField(field[0], field[1]); err != nil {
					return err
				}
			}
			// A fixed safe basename avoids disclosing the original path or radio metadata.
			name := "recording.mp3"
			if strings.HasSuffix(strings.ToLower(f.Name()), ".wav") {
				name = "recording.wav"
			}
			part, err := form.CreateFormFile("file", name)
			if err != nil {
				return err
			}
			if _, err = io.CopyN(part, f, info.Size()); err != nil {
				return err
			}
			return form.Close()
		}()
		writer.CloseWithError(err)
	}()
	defer func() { reader.Close(); <-done }()
	started := time.Now()
	if measurement, ok := parent.Value(measurementKey{}).(*RequestMeasurement); ok {
		measurement.StartedAt = started.UTC()
		defer func() { measurement.Duration = time.Since(started) }()
	}
	resp, err := p.client.Do(req)
	if err != nil {
		if parent.Err() != nil {
			return fail("canceled", true)
		}
		if ctx.Err() != nil {
			return fail("provider_timeout", true)
		}
		return fail("provider_unavailable", true)
	}
	defer resp.Body.Close()
	limit := p.options.MaxResponseBytes
	if resp.StatusCode != http.StatusOK && limit > 4096 {
		limit = 4096
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if parent.Err() != nil {
			return fail("canceled", true)
		}
		if ctx.Err() != nil {
			return fail("provider_timeout", true)
		}
		return fail("provider_unavailable", true)
	}
	if resp.StatusCode != http.StatusOK {
		return fail("provider_rejected", resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500)
	}
	if int64(len(body)) > limit || !utf8.Valid(body) {
		return fail("invalid_response", false)
	}
	var payload struct {
		Text *string `json:"text"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Text == nil {
		return fail("invalid_response", false)
	}
	raw := *payload.Text
	if !utf8.ValidString(raw) || strings.ContainsRune(raw, '\ufffd') || strings.IndexFunc(raw, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' || unicode.In(r, unicode.Cf)
	}) >= 0 {
		return fail("invalid_response", false)
	}
	normalized := strings.Join(strings.Fields(raw), " ")
	if normalized == "" {
		return fail("invalid_response", false)
	}
	return Result{RawText: raw, Text: normalized}, nil
}
