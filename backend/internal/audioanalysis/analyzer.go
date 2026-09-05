package audioanalysis

import (
	"context"
	"io"
)

type Analyzer struct {
	Options Options
	Tools   Tools
}

func (a Analyzer) Analyze(ctx context.Context, input io.ReadSeeker) (Result, error) {
	if input == nil {
		return Result{}, errSource
	}
	ctx, cancel := context.WithTimeout(ctx, a.Options.Timeout)
	defer cancel()
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return Result{}, errSource
	}
	probeOutput := &boundedBuffer{limit: 64 * 1024}
	args := []string{"-v", "error", "-max_alloc", "67108864", "-protocol_whitelist", "pipe",
		"-format_whitelist", "mp3,wav", "-show_entries", "format=format_name,duration:stream=codec_type,codec_name,sample_rate,channels,duration",
		"-of", "json", "-i", "pipe:0"}
	if err := a.Tools.Run(ctx, a.Options.FFprobePath, args, input, probeOutput); err != nil {
		return Result{}, safeFailure(err)
	}
	if probeOutput.truncated {
		return Result{}, errInvalid
	}
	probe, err := ParseProbe(probeOutput.Bytes(), a.Options.MaxDuration)
	if err != nil {
		return Result{}, err
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return Result{}, errSource
	}
	pcm := newPCM(a.Options.MaxDuration)
	args = []string{"-v", "error", "-nostdin", "-xerror", "-max_alloc", "67108864",
		"-threads", "1", "-protocol_whitelist", "pipe", "-format_whitelist", "mp3,wav", "-i", "pipe:0",
		"-map", "0:a:0", "-vn", "-sn", "-dn", "-map_metadata", "-1", "-ac", "1", "-ar", "16000",
		"-c:a", "pcm_s16le", "-threads", "1", "-f", "s16le", "pipe:1"}
	if err := a.Tools.Run(ctx, a.Options.FFmpegPath, args, input, pcm); err != nil {
		if pcm.err != nil {
			return Result{}, pcm.err
		}
		return Result{}, safeFailure(err)
	}
	result, err := pcm.result()
	result.Probe = probe
	return result, err
}
