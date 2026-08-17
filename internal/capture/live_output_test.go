package capture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/MorisKyiv/runprint/internal/record"
)

const liveOutputHelperMarker = "runprint-live-output-child"

func TestRunWithOutputMirrorsAndCapturesStreams(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	interrupts := make(chan os.Signal, 1)
	r, status, err := RunWithOutput(
		context.Background(),
		liveOutputHelperCommand("basic"),
		interrupts,
		&stdout,
		&stderr,
		record.DevelopmentVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", r.ExitCode)
	}
	if got, want := stdout.String(), "live stdout"; got != want {
		t.Fatalf("live stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); !strings.HasSuffix(got, "live stderr") {
		t.Fatalf("live stderr = %q, want child output suffix", got)
	}
	if got, want := r.Stdout.HeadText, stdout.String(); got != want {
		t.Fatalf("captured stdout = %q, want %q", got, want)
	}
	if got, want := r.Stderr.HeadText, stderr.String(); got != want {
		t.Fatalf("captured stderr = %q, want %q", got, want)
	}
	if status.StdoutFailed || status.StderrFailed {
		t.Fatalf("passthrough status = %+v, want no failures", status)
	}
}

func TestRunWithOutputCapturesAfterPassthroughFailure(t *testing.T) {
	stdout := &rejectingWriter{}
	interrupts := make(chan os.Signal, 1)
	r, status, err := RunWithOutput(
		context.Background(),
		liveOutputHelperCommand("large"),
		interrupts,
		stdout,
		io.Discard,
		record.DevelopmentVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", r.ExitCode)
	}
	if !status.StdoutFailed || status.StderrFailed {
		t.Fatalf("passthrough status = %+v, want only stdout failure", status)
	}
	if stdout.calls != 1 {
		t.Fatalf("passthrough writes = %d, want exactly one before disable", stdout.calls)
	}
	if got, want := int64(r.Stdout.ReceivedBytes), int64(300<<10); got != want {
		t.Fatalf("captured bytes = %d, want %d", got, want)
	}
	if !r.Stdout.CaptureComplete {
		t.Fatal("capture was marked incomplete after only passthrough failed")
	}
	if r.Stdout.OmittedBytes == 0 {
		t.Fatal("large captured stream was not bounded")
	}
}

func TestCaptureSinkDisablesShortPassthroughWriter(t *testing.T) {
	collector := newStreamCollector(defaultHeadBytes, defaultTailBytes)
	sink := &captureSink{collector: collector, passthrough: shortWriter{}}
	data := []byte("captured first")

	written, err := sink.Write(data)
	if err != nil {
		t.Fatal(err)
	}
	if written != len(data) {
		t.Fatalf("written = %d, want %d", written, len(data))
	}
	if !sink.passthroughFailed.Load() {
		t.Fatal("short passthrough writer was not disabled")
	}
	if got := string(collector.snapshot().head); got != string(data) {
		t.Fatalf("captured data = %q, want %q", got, data)
	}
}

type rejectingWriter struct {
	calls int
}

func (writer *rejectingWriter) Write(_ []byte) (int, error) {
	writer.calls++
	return 0, errors.New("injected passthrough failure")
}

type shortWriter struct{}

func (shortWriter) Write(data []byte) (int, error) {
	return len(data) / 2, nil
}

func liveOutputHelperCommand(mode string) []string {
	return []string{
		os.Args[0],
		"-test.run=^TestLiveOutputChildHelper$",
		"--", liveOutputHelperMarker, mode,
	}
}

func TestLiveOutputChildHelper(t *testing.T) {
	mode, ok := liveOutputHelperMode()
	if !ok {
		return
	}
	switch mode {
	case "basic":
		fmt.Fprint(os.Stdout, "live stdout")
		fmt.Fprint(os.Stderr, "live stderr")
		os.Exit(7)
	case "large":
		chunk := bytes.Repeat([]byte("x"), 300<<10)
		if _, err := os.Stdout.Write(chunk); err != nil {
			os.Exit(98)
		}
		os.Exit(0)
	default:
		os.Exit(99)
	}
}

func liveOutputHelperMode() (string, bool) {
	for index := 0; index+2 < len(os.Args); index++ {
		if os.Args[index] == "--" && os.Args[index+1] == liveOutputHelperMarker {
			return os.Args[index+2], true
		}
	}
	return "", false
}
