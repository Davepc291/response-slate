package audioanalysis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"
	"time"
)

const validMP3 = `{"format":{"format_name":"mp3","duration":"1.25"},"streams":[{"codec_type":"audio","codec_name":"mp3","sample_rate":"8000","channels":1}]}`
const validWAV = `{"format":{"format_name":"wav"},"streams":[{"codec_type":"audio","codec_name":"pcm_s16le","sample_rate":"16000","channels":1}]}`

func TestParseProbe(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		valid       bool
	}{
		{"mp3", validMP3, true}, {"wav duration unknown on pipe", validWAV, true},
		{"corrupt JSON", "{", false}, {"empty", "", false},
		{"unsupported format", strings.Replace(validMP3, "mp3", "ogg", 1), false},
		{"video only", strings.Replace(validMP3, "audio", "video", 1), false},
		{"multiple audio", `{"format":{"format_name":"mp3"},"streams":[{"codec_type":"audio"},{"codec_type":"audio"}]}`, false},
		{"no audio", `{"format":{"format_name":"mp3"},"streams":[]}`, false},
		{"unusable sample rate", strings.Replace(validMP3, "8000", "0", 1), false},
		{"unknown codec", strings.Replace(validWAV, "pcm_s16le", "unknown", 1), false},
		{"zero duration", strings.Replace(validMP3, "1.25", "0", 1), false},
		{"excessive duration", strings.Replace(validMP3, "1.25", "601", 1), false},
		{"nonfinite duration", strings.Replace(validMP3, "1.25", "NaN", 1), false},
		{"oversized JSON", strings.Repeat(" ", 65537), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ParseProbe([]byte(tc.input), 10*time.Minute)
			if (err == nil) != tc.valid {
				t.Fatalf("unexpected result: %+v %v", p, err)
			}
		})
	}
	p, _ := ParseProbe([]byte(validMP3), time.Minute)
	if p.DurationMS == nil || *p.DurationMS != 1250 {
		t.Fatal("probe duration not parsed")
	}
}

func TestPCMStatistics(t *testing.T) {
	for _, tc := range []struct {
		name      string
		values    []int16
		rms, peak float64
	}{
		{"silence", []int16{0, 0}, 0, 0},
		{"positive max", []int16{32767}, 32767.0 / 32768, 32767.0 / 32768},
		{"negative max", []int16{-32768}, 1, 1},
		{"mixed", []int16{-32768, 0}, math.Sqrt(.5), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, len(tc.values)*2)
			for i, v := range tc.values {
				binary.LittleEndian.PutUint16(data[i*2:], uint16(v))
			}
			p := newPCM(time.Minute)
			p.Write(data)
			got, err := p.result()
			if err != nil || math.Abs(got.RMS-tc.rms) > 1e-12 || got.Peak != tc.peak || got.Fingerprint != FingerprintPrefix+fmt.Sprintf("%x", sha256.Sum256(data)) {
				t.Fatalf("unexpected PCM result: %+v %v", got, err)
			}
		})
	}
}

func TestPCMChunkBoundariesAndDuration(t *testing.T) {
	data := make([]byte, 32002)
	for i := range data {
		data[i] = byte(i % 251)
	}
	var baseline Result
	for _, chunk := range []int{1, 3, 31, 32768} {
		p := newPCM(time.Minute)
		for i := 0; i < len(data); i += chunk {
			end := min(i+chunk, len(data))
			p.Write(data[i:end])
		}
		r, err := p.result()
		if err != nil {
			t.Fatal(err)
		}
		if chunk == 1 {
			baseline = r
		} else if r != baseline {
			t.Fatal("result depends on chunk boundaries")
		}
	}
	if baseline.DurationMS != 1000 {
		t.Fatalf("rounded duration = %d", baseline.DurationMS)
	}
	for _, data := range [][]byte{nil, {1}} {
		p := newPCM(time.Second)
		p.Write(data)
		if _, err := p.result(); err == nil {
			t.Fatal("incomplete PCM accepted")
		}
	}
	p := newPCM(time.Second)
	if _, err := p.Write(make([]byte, 32002)); err != errDuration {
		t.Fatal("PCM size limit not enforced")
	}
}

type fakeTools struct {
	probe string
	pcm   []byte
	err   error
	calls int
	args  [][]string
}

func (f *fakeTools) Run(ctx context.Context, path string, args []string, in io.Reader, out io.Writer) error {
	f.calls++
	f.args = append(f.args, args)
	if _, ok := ctx.Deadline(); !ok {
		return errTimeout
	}
	if f.err != nil {
		return f.err
	}
	if f.calls%2 == 1 {
		_, err := io.WriteString(out, f.probe)
		return err
	}
	for _, b := range f.pcm {
		if _, err := out.Write([]byte{b}); err != nil {
			return err
		}
	}
	return nil
}

func TestAnalyzer(t *testing.T) {
	for _, probe := range []string{validMP3, validWAV} {
		tools := &fakeTools{probe: probe, pcm: []byte{0, 128, 255, 127}}
		a := Analyzer{DefaultOptions(), tools}
		r, err := a.Analyze(context.Background(), bytes.NewReader([]byte("synthetic encoded input")))
		if err != nil || r.Peak != 1 || tools.calls != 2 {
			t.Fatalf("analysis failed: %+v %v", r, err)
		}
		args := strings.Join(tools.args[1], " ")
		for _, option := range []string{"-i pipe:0", "-ac 1", "-ar 16000", "-c:a pcm_s16le", "-f s16le pipe:1", "-protocol_whitelist pipe"} {
			if !strings.Contains(args, option) {
				t.Fatal("missing canonical/safety option")
			}
		}
	}
	for _, tc := range []struct {
		name, probe string
		pcm         []byte
		err         error
	}{
		{"corrupt", "broken", nil, nil}, {"empty decoded", validMP3, nil, nil}, {"missing tool", validMP3, nil, errUnavailable},
		{"timeout", validMP3, nil, errTimeout}, {"canceled", validMP3, nil, errCanceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := Analyzer{DefaultOptions(), &fakeTools{probe: tc.probe, pcm: tc.pcm, err: tc.err}}
			if _, err := a.Analyze(context.Background(), bytes.NewReader(nil)); err == nil {
				t.Fatal("invalid analysis accepted")
			}
		})
	}
}
