package audioanalysis

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
)

// Tools accepts argument vectors and streams; implementations must honor context.
type Tools interface {
	Run(context.Context, string, []string, io.Reader, io.Writer) error
}

type ProcessTools struct{}

type boundedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.Len()
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, _ = b.Buffer.Write(p)
	return n, nil // Drain excess stderr rather than blocking a noisy child.
}

type cancelWriter struct {
	output io.Writer
	cancel context.CancelFunc
	err    error
}

func (w *cancelWriter) Write(p []byte) (int, error) {
	n, err := w.output.Write(p)
	if err != nil {
		w.err = err
		w.cancel()
	}
	return n, err
}

func (ProcessTools) Run(ctx context.Context, executable string, args []string, input io.Reader, output io.Writer) error {
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(childCtx, executable, args...)
	configureCommand(cmd)
	cmd.WaitDelay = time.Second
	cmd.Stdin = input
	w := &cancelWriter{output: output, cancel: cancel}
	cmd.Stdout = w
	stderr := &boundedBuffer{limit: 16 * 1024}
	cmd.Stderr = stderr
	// No database settings, FFREPORT, or other application environment is inherited.
	cmd.Env = []string{"AV_LOG_FORCE_NOCOLOR=1"}
	for _, key := range []string{"SYSTEMROOT", "WINDIR", "PATH", "TEMP", "TMP"} {
		if value := os.Getenv(key); value != "" {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	err := cmd.Run()
	if w.err != nil {
		return safeFailure(w.err)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errTimeout
	}
	if ctx.Err() != nil {
		return errCanceled
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return errInvalid
		}
		return errUnavailable
	}
	// Error-level decoder diagnostics make even a zero-exit decode untrustworthy.
	if stderr.Len() > 0 {
		return errInvalid
	}
	return nil
}
