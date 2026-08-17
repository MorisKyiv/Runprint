//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package capture

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type runResponse struct {
	record recordResult
	err    error
}

type recordResult struct {
	exitCode        int
	interruptSignal string
	stdout          string
	stderr          string
	stdoutComplete  bool
	stderrComplete  bool
}

func TestRunForwardsInterruptAndReturnsConventionalCode(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	interrupts := make(chan os.Signal, 2)
	result := make(chan runResponse, 1)

	go func() {
		r, err := run(
			context.Background(),
			[]string{
				os.Args[0],
				"-test.run=^TestInterruptibleChildHelper$",
				"--", "runprint-interrupt-child", ready,
			},
			interrupts,
			runPolicy{interruptGrace: time.Second, drainGrace: 250 * time.Millisecond},
		)
		response := runResponse{err: err}
		if err == nil {
			interruptSignal := ""
			if r.Interruption != nil {
				interruptSignal = r.Interruption.Signal
			}
			response.record = recordResult{
				exitCode:        r.ExitCode,
				interruptSignal: interruptSignal,
				stdout:          r.Stdout.HeadText,
				stderr:          r.Stderr.HeadText,
				stdoutComplete:  r.Stdout.CaptureComplete,
				stderrComplete:  r.Stderr.CaptureComplete,
			}
		}
		result <- response
	}()

	waitForFile(t, ready)
	interrupts <- os.Interrupt

	response := waitForRun(t, result)
	if response.err != nil {
		t.Fatal(response.err)
	}
	if response.record.exitCode != 130 {
		t.Fatalf("exit code = %d, want 130", response.record.exitCode)
	}
	if response.record.interruptSignal != "SIGINT" {
		t.Fatalf("interruption signal = %q, want SIGINT", response.record.interruptSignal)
	}
	if response.record.stdout != "started" {
		t.Fatalf("stdout = %q, want started", response.record.stdout)
	}
	if !strings.Contains(response.record.stderr, "interrupted") {
		t.Fatalf("stderr = %q, want interrupt handler output", response.record.stderr)
	}
	if !response.record.stdoutComplete || !response.record.stderrComplete {
		t.Fatalf(
			"capture complete = stdout:%t stderr:%t, want both true",
			response.record.stdoutComplete,
			response.record.stderrComplete,
		)
	}
}

func TestRunAbandonsBlockedPassthroughAfterInterruptedDrainDeadline(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	interrupts := make(chan os.Signal, 1)
	result := make(chan runResponse, 1)
	blocked := newBlockingWriter()
	t.Cleanup(blocked.release)
	status := PassthroughStatus{}

	go func() {
		r, err := run(
			context.Background(),
			[]string{
				os.Args[0],
				"-test.run=^TestInterruptibleChildHelper$",
				"--", "runprint-interrupt-child", ready,
			},
			interrupts,
			runPolicy{
				interruptGrace:    time.Second,
				drainGrace:        50 * time.Millisecond,
				stdoutPassthrough: blocked,
				stderrPassthrough: io.Discard,
				passthroughStatus: &status,
			},
		)
		response := runResponse{err: err}
		if err == nil {
			response.record = recordResult{
				exitCode:       r.ExitCode,
				stdout:         r.Stdout.HeadText,
				stdoutComplete: r.Stdout.CaptureComplete,
			}
		}
		result <- response
	}()

	waitForFile(t, ready)
	blocked.waitUntilStarted(t)
	started := time.Now()
	interrupts <- os.Interrupt

	response := waitForRun(t, result)
	blocked.release()
	if response.err != nil {
		t.Fatal(response.err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("blocked passthrough delayed interrupted return by %s, want under 2s", elapsed)
	}
	if response.record.exitCode != 130 {
		t.Fatalf("exit code = %d, want 130", response.record.exitCode)
	}
	if response.record.stdout != "started" {
		t.Fatalf("captured stdout = %q, want started", response.record.stdout)
	}
	if response.record.stdoutComplete {
		t.Fatal("abandoned stdout drain was marked complete")
	}
	if !status.StdoutFailed || status.StderrFailed {
		t.Fatalf("passthrough status = %+v, want only stdout failure", status)
	}
}

func TestRunDoesNotLeakSIGPIPESuppressionIntoChildPipeline(t *testing.T) {
	sigpipe := make(chan os.Signal, 1)
	signal.Notify(sigpipe, syscall.SIGPIPE)
	defer signal.Stop(sigpipe)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	r, err := RunWithInterrupts(ctx, []string{"sh", "-c", "yes | head -1"}, make(chan os.Signal))
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", r.ExitCode)
	}
	if r.Stdout.HeadText != "y\n" {
		t.Fatalf("stdout = %q, want y newline", r.Stdout.HeadText)
	}
	if !r.Stdout.CaptureComplete || !r.Stderr.CaptureComplete {
		t.Fatalf(
			"capture complete = stdout:%t stderr:%t, want both true",
			r.Stdout.CaptureComplete,
			r.Stderr.CaptureComplete,
		)
	}
}

func TestInterruptibleChildHelper(t *testing.T) {
	ready, ok := helperArgument("runprint-interrupt-child")
	if !ok {
		return
	}
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)

	fmt.Print("started")
	if err := os.WriteFile(ready, []byte("ready\n"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(98)
	}
	<-interrupts
	fmt.Fprint(os.Stderr, "interrupted")
	os.Exit(0)
}

type blockingWriter struct {
	started     chan struct{}
	releaseGate chan struct{}
	startOnce   sync.Once
	releaseOnce sync.Once
}

func newBlockingWriter() *blockingWriter {
	return &blockingWriter{
		started:     make(chan struct{}),
		releaseGate: make(chan struct{}),
	}
}

func (writer *blockingWriter) Write(data []byte) (int, error) {
	writer.startOnce.Do(func() { close(writer.started) })
	<-writer.releaseGate
	return len(data), nil
}

func (writer *blockingWriter) waitUntilStarted(t *testing.T) {
	t.Helper()
	select {
	case <-writer.started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for passthrough write")
	}
}

func (writer *blockingWriter) release() {
	writer.releaseOnce.Do(func() { close(writer.releaseGate) })
}

func TestRunForcesProcessGroupAfterInterruptGrace(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	interrupts := make(chan os.Signal, 2)
	result := make(chan runResponse, 1)
	started := time.Now()

	go func() {
		r, err := run(
			context.Background(),
			[]string{
				"sh", "-c",
				`trap '' INT; : > "$1"; while :; do sleep 30; done`,
				"runprint-test", ready,
			},
			interrupts,
			runPolicy{interruptGrace: 50 * time.Millisecond, drainGrace: 250 * time.Millisecond},
		)
		response := runResponse{err: err}
		if err == nil {
			response.record.exitCode = r.ExitCode
			response.record.stdoutComplete = r.Stdout.CaptureComplete
			response.record.stderrComplete = r.Stderr.CaptureComplete
		}
		result <- response
	}()

	waitForFile(t, ready)
	interrupts <- os.Interrupt
	response := waitForRun(t, result)

	if response.err != nil {
		t.Fatal(response.err)
	}
	if response.record.exitCode != 130 {
		t.Fatalf("exit code = %d, want 130", response.record.exitCode)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("forced interruption took %s, want under 2s", elapsed)
	}
	if !response.record.stdoutComplete || !response.record.stderrComplete {
		t.Fatal("forced process-group termination did not close captured streams")
	}
}

func TestRunUsesFirstSignalForInterruptedExitCode(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	interrupts := make(chan os.Signal, 2)
	result := make(chan runResponse, 1)

	go func() {
		r, err := run(
			context.Background(),
			[]string{
				"sh", "-c",
				`trap '' TERM; : > "$1"; while :; do sleep 30; done`,
				"runprint-test", ready,
			},
			interrupts,
			runPolicy{interruptGrace: 5 * time.Second, drainGrace: 250 * time.Millisecond},
		)
		response := runResponse{err: err}
		if err == nil {
			response.record.exitCode = r.ExitCode
		}
		result <- response
	}()

	waitForFile(t, ready)
	started := time.Now()
	interrupts <- syscall.SIGTERM
	interrupts <- os.Interrupt
	response := waitForRun(t, result)

	if response.err != nil {
		t.Fatal(response.err)
	}
	if response.record.exitCode != 143 {
		t.Fatalf("exit code = %d, want 143 from first SIGTERM", response.record.exitCode)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("second signal did not bypass grace period; elapsed %s", elapsed)
	}
}

func TestRunMapsUncaughtChildSignalToShellExitCode(t *testing.T) {
	r, err := Run(context.Background(), []string{"sh", "-c", "kill -TERM $$"})
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 143 {
		t.Fatalf("exit code = %d, want 143", r.ExitCode)
	}
	if r.Interruption != nil {
		t.Fatalf("self-signaled command recorded as Runprint interruption: %#v", r.Interruption)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func waitForRun(t *testing.T, result <-chan runResponse) runResponse {
	t.Helper()
	select {
	case response := <-result:
		return response
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for interrupted command")
		return runResponse{}
	}
}

func helperArgument(marker string) (string, bool) {
	for index := 0; index+2 < len(os.Args); index++ {
		if os.Args[index] == "--" && os.Args[index+1] == marker {
			return os.Args[index+2], true
		}
	}
	return "", false
}
