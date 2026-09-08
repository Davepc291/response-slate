package transcriptexperiment

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const Endpoint = "http://127.0.0.1:8001/v1/audio/transcriptions"
const Model = "small.en"
const MaxAudioBytes = 32 << 20
const MaxResponseBytes = 128 << 10

type Outcome struct {
	DatasetID    string  `json:"dataset_id"`
	SourceSHA256 string  `json:"source_sha256"`
	HTTPStatus   int     `json:"http_status"`
	DurationMS   float64 `json:"request_ms"`
	Result       string  `json:"result"`
}
type Sender interface {
	Send(context.Context, []byte, string, string) (string, Outcome, error)
	Close()
}
type provider struct {
	client  *http.Client
	timeout time.Duration
}

func newProvider(timeout time.Duration, transport http.RoundTripper) *provider {
	if transport == nil {
		transport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, DisableKeepAlives: true, ResponseHeaderTimeout: timeout, MaxResponseHeaderBytes: 16384}
	}
	return &provider{&http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, timeout}
}
func NewProvider(timeout time.Duration) (Sender, error) {
	if timeout < time.Second || timeout > 2*time.Minute {
		return nil, ErrInput
	}
	return newProvider(timeout, nil), nil
}
func (p *provider) Close() { p.client.CloseIdleConnections() }
func (p *provider) Send(parent context.Context, audio []byte, extension, prompt string) (text string, out Outcome, err error) {
	out.Result = "failed"
	if len(audio) == 0 || len(audio) > MaxAudioBytes || (extension != ".mp3" && extension != ".wav") {
		return "", out, ErrInput
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	fields := [][2]string{{"model", Model}, {"language", "en"}, {"response_format", "json"}, {"temperature", "0"}, {"beam_size", "5"}, {"best_of", "5"}, {"word_boost", ""}}
	if prompt != "" {
		fields = append(fields, [2]string{"prompt", prompt})
	}
	for _, f := range fields {
		if form.WriteField(f[0], f[1]) != nil {
			return "", out, ErrInput
		}
	}
	part, e := form.CreateFormFile("file", "recording"+extension)
	if e != nil {
		return "", out, ErrInput
	}
	if _, e = part.Write(audio); e != nil {
		return "", out, ErrInput
	}
	if form.Close() != nil {
		return "", out, ErrInput
	}
	ctx, cancel := context.WithTimeout(parent, p.timeout)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, Endpoint, &body)
	if e != nil {
		return "", out, ErrRequest
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	started := time.Now()
	defer func() { out.DurationMS = float64(time.Since(started)) / float64(time.Millisecond) }()
	response, e := p.client.Do(req)
	if e != nil {
		return "", out, ErrRequest
	}
	defer response.Body.Close()
	out.HTTPStatus = response.StatusCode
	if response.StatusCode != 200 {
		return "", out, ErrRequest
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if e != nil || len(data) > MaxResponseBytes || !utf8.Valid(data) || !json.Valid(data) {
		return "", out, ErrRequest
	}
	// Only one non-null text property is accepted; duplicate keys are ambiguous.
	dec := json.NewDecoder(bytes.NewReader(data))
	token, e := dec.Token()
	if e != nil || token != json.Delim('{') {
		return "", out, ErrRequest
	}
	found := false
	seen := map[string]bool{}
	for dec.More() {
		key, e := dec.Token()
		if e != nil {
			return "", out, ErrRequest
		}
		k, ok := key.(string)
		if !ok || seen[k] {
			return "", out, ErrRequest
		}
		seen[k] = true
		var raw json.RawMessage
		if dec.Decode(&raw) != nil {
			return "", out, ErrRequest
		}
		if k == "text" {
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &text) != nil {
				return "", out, ErrRequest
			}
			found = true
		}
	}
	if !found || strings.ContainsRune(text, '\ufffd') || !safeText(text, 65536) {
		return "", out, ErrRequest
	}
	out.Result = "ok"
	return text, out, nil
}
