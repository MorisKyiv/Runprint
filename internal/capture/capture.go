package capture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MorisKyiv/runprint/internal/record"
)

const (
	defaultInterruptGrace = 2 * time.Second
	defaultDrainGrace     = 500 * time.Millisecond
)

type runPolicy struct {
	interruptGrace    time.Duration
	drainGrace        time.Duration
	runprintVersion   string
	stdoutPassthrough io.Writer
	stderrPassthrough io.Writer
	passthroughStatus *PassthroughStatus
	preflight         func() error
}

// PassthroughStatus reports whether live output forwarding stopped or was
// abandoned for either stream. Capture remains authoritative.
type PassthroughStatus struct {
	StdoutFailed bool
	StderrFailed bool
}

// StartError reports that the requested command never began executing. The
// conventional shell status is 127 when no executable was found and 126 when
// an executable was found but could not be invoked.
type StartError struct {
	Command string
	Err     error
}

func (err *StartError) Error() string {
	return fmt.Sprintf("start command %q: %v", err.Command, err.Err)
}

func (err *StartError) Unwrap() error {
	return err.Err
}

func (err *StartError) ExitCode() int {
	if errors.Is(err.Err, exec.ErrNotFound) || errors.Is(err.Err, os.ErrNotExist) {
		return 127
	}
	return 126
}

func Run(ctx context.Context, argv []string) (record.Record, error) {
	return run(ctx, argv, nil, runPolicy{
		interruptGrace:  defaultInterruptGrace,
		drainGrace:      defaultDrainGrace,
		runprintVersion: record.DevelopmentVersion,
	})
}

// RunWithInterrupts records a command while forwarding received termination
// signals to an isolated child process group. The first signal gets a grace
// period; a second signal or the grace deadline forces termination.
func RunWithInterrupts(ctx context.Context, argv []string, interrupts <-chan os.Signal) (record.Record, error) {
	return run(ctx, argv, interrupts, runPolicy{
		interruptGrace:  defaultInterruptGrace,
		drainGrace:      defaultDrainGrace,
		runprintVersion: record.DevelopmentVersion,
	})
}

// RunWithOutput records a command, forwards termination signals, and mirrors
// stdout/stderr live. Passthrough is best effort and never controls capture.
func RunWithOutput(
	ctx context.Context,
	argv []string,
	interrupts <-chan os.Signal,
	stdoutPassthrough io.Writer,
	stderrPassthrough io.Writer,
	runprintVersion string,
) (record.Record, PassthroughStatus, error) {
	return RunWithOutputAndPreflight(
		ctx,
		argv,
		interrupts,
		stdoutPassthrough,
		stderrPassthrough,
		runprintVersion,
		nil,
	)
}

// RunWithOutputAndPreflight is RunWithOutput with a preflight hook that runs
// after Git context is captured but before the command starts. Integrations
// use it to prove that the record destination is writable without making that
// destination appear as pre-existing Git state.
func RunWithOutputAndPreflight(
	ctx context.Context,
	argv []string,
	interrupts <-chan os.Signal,
	stdoutPassthrough io.Writer,
	stderrPassthrough io.Writer,
	runprintVersion string,
	preflight func() error,
) (record.Record, PassthroughStatus, error) {
	status := PassthroughStatus{}
	r, err := run(ctx, argv, interrupts, runPolicy{
		interruptGrace:    defaultInterruptGrace,
		drainGrace:        defaultDrainGrace,
		runprintVersion:   runprintVersion,
		stdoutPassthrough: stdoutPassthrough,
		stderrPassthrough: stderrPassthrough,
		passthroughStatus: &status,
		preflight:         preflight,
	})
	return r, status, err
}

// HandledSignals returns the platform signals record mode should subscribe to.
func HandledSignals() []os.Signal {
	return handledSignals()
}

func run(ctx context.Context, argv []string, interrupts <-chan os.Signal, policy runPolicy) (record.Record, error) {
	if len(argv) == 0 {
		return record.Record{}, errors.New("no command provided")
	}

	dir, err := os.Getwd()
	if err != nil {
		return record.Record{}, err
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	isolatedProcessGroup := interrupts != nil
	prepareCommand(cmd, isolatedProcessGroup)

	stdout := newStreamCollector(defaultHeadBytes, defaultTailBytes)
	stderr := newStreamCollector(defaultHeadBytes, defaultTailBytes)
	stdoutSink := &captureSink{
		collector:             stdout,
		passthrough:           policy.stdoutPassthrough,
		passthroughConfigured: policy.stdoutPassthrough != nil,
	}
	stderrSink := &captureSink{
		collector:             stderr,
		passthrough:           policy.stderrPassthrough,
		passthroughConfigured: policy.stderrPassthrough != nil,
	}
	pipes, err := attachCapturePipes(cmd, stdoutSink, stderrSink)
	if err != nil {
		return record.Record{}, err
	}
	defer pipes.close()

	git := gitContext(ctx, dir)
	if policy.preflight != nil {
		if err := policy.preflight(); err != nil {
			return record.Record{}, err
		}
	}
	startedClock := time.Now()
	started := startedClock.UTC()
	if policy.runprintVersion == "" {
		policy.runprintVersion = record.DevelopmentVersion
	}
	metadata := record.Record{
		Version:         record.CurrentVersion,
		RunprintVersion: policy.runprintVersion,
		Command:         append([]string(nil), argv...),
		Directory:       portableDirectory(dir),
		StartedAt:       started,
		Stdout:          record.NewStream(0, true, nil, 0, nil),
		Stderr:          record.NewStream(0, true, nil, 0, nil),
		Git:             git,
	}
	if err := metadata.Validate(); err != nil {
		return record.Record{}, fmt.Errorf("record metadata: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return record.Record{}, &StartError{Command: argv[0], Err: err}
	}
	pipes.closeWriters()
	drains := pipes.drain()

	waitResult := make(chan error, 1)
	var reaped atomic.Bool
	go func() {
		err := cmd.Wait()
		reaped.Store(true)
		waitResult <- err
	}()
	waitErr, interrupted, cancelled, ended := waitForCommand(
		ctx,
		cmd,
		waitResult,
		&reaped,
		interrupts,
		isolatedProcessGroup,
		policy.interruptGrace,
	)
	stdoutComplete, stderrComplete := waitForDrains(
		pipes,
		drains,
		policy.drainGrace,
		interrupted != nil || cancelled,
	)
	if policy.passthroughStatus != nil {
		policy.passthroughStatus.StdoutFailed = stdoutSink.passthroughFailed.Load()
		policy.passthroughStatus.StderrFailed = stderrSink.passthroughFailed.Load()
	}
	duration := ended.Sub(startedClock)

	exitCode, err := commandExitCode(waitErr)
	if err != nil {
		return record.Record{}, err
	}
	if interrupted != nil {
		if interruptedCode, ok := signalExitCode(interrupted); ok {
			exitCode = interruptedCode
		}
	}

	stdoutSnapshot := stdout.snapshot()
	stderrSnapshot := stderr.snapshot()
	r := record.Record{
		Version:         record.CurrentVersion,
		RunprintVersion: policy.runprintVersion,
		Command:         append([]string(nil), argv...),
		Directory:       metadata.Directory,
		StartedAt:       started,
		Duration:        duration,
		ExitCode:        exitCode,
		Stdout: record.NewStream(
			stdoutSnapshot.received,
			stdoutComplete,
			stdoutSnapshot.head,
			stdoutSnapshot.omitted,
			stdoutSnapshot.tail,
		),
		Stderr: record.NewStream(
			stderrSnapshot.received,
			stderrComplete,
			stderrSnapshot.head,
			stderrSnapshot.omitted,
			stderrSnapshot.tail,
		),
		Git: git,
	}
	if interrupted != nil {
		r.Interruption = &record.Interruption{Signal: portableSignalName(interrupted)}
	}

	return r, nil
}

func commandExitCode(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code, ok := processExitCode(exitErr.ProcessState); ok {
			return code, nil
		}
		return exitErr.ExitCode(), nil
	}
	return 0, err
}

func waitForCommand(
	ctx context.Context,
	cmd *exec.Cmd,
	waitResult <-chan error,
	reaped *atomic.Bool,
	interrupts <-chan os.Signal,
	isolatedProcessGroup bool,
	interruptGrace time.Duration,
) (error, os.Signal, bool, time.Time) {
	var firstInterrupt os.Signal
	contextCancelled := false
	var timer *time.Timer
	var deadline <-chan time.Time
	contextDone := ctx.Done()

	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		if reaped.Load() {
			return <-waitResult, firstInterrupt, contextCancelled, time.Now()
		}
		select {
		case err := <-waitResult:
			return err, firstInterrupt, contextCancelled, time.Now()
		case sig, ok := <-interrupts:
			if !ok {
				interrupts = nil
				continue
			}
			if sig == nil {
				continue
			}
			if reaped.Load() {
				return <-waitResult, firstInterrupt, contextCancelled, time.Now()
			}

			// Prefer a command result already available over a late signal.
			select {
			case err := <-waitResult:
				return err, firstInterrupt, contextCancelled, time.Now()
			default:
			}

			if firstInterrupt == nil {
				// Wait reaps the process before it can publish its result. The
				// reaped flag narrows the interval in which a late signal could
				// target a reused process-group ID. Eliminating that interval
				// requires waitid(WNOWAIT), which Go's portable stdlib does not
				// expose; the remaining PID-reuse window is accepted here.
				firstInterrupt = sig
				if err := forwardSignal(cmd, sig, isolatedProcessGroup); err != nil {
					_ = forceKill(cmd, isolatedProcessGroup)
					continue
				}
				if interruptGrace <= 0 {
					_ = forceKill(cmd, isolatedProcessGroup)
					continue
				}
				timer = time.NewTimer(interruptGrace)
				deadline = timer.C
				continue
			}

			_ = forceKill(cmd, isolatedProcessGroup)
			if timer != nil {
				timer.Stop()
			}
			deadline = nil
		case <-deadline:
			if reaped.Load() {
				return <-waitResult, firstInterrupt, contextCancelled, time.Now()
			}
			select {
			case err := <-waitResult:
				return err, firstInterrupt, contextCancelled, time.Now()
			default:
			}
			_ = forceKill(cmd, isolatedProcessGroup)
			deadline = nil
		case <-contextDone:
			if reaped.Load() {
				return <-waitResult, firstInterrupt, contextCancelled, time.Now()
			}
			select {
			case err := <-waitResult:
				return err, firstInterrupt, contextCancelled, time.Now()
			default:
			}
			contextCancelled = true
			_ = forceKill(cmd, isolatedProcessGroup)
			contextDone = nil
		}
	}
}

type capturePipes struct {
	stdoutReader *os.File
	stdoutWriter *os.File
	stderrReader *os.File
	stderrWriter *os.File
	stdout       *captureSink
	stderr       *captureSink
}

type captureSink struct {
	collector             *streamCollector
	passthrough           io.Writer
	passthroughConfigured bool
	passthroughFailed     atomic.Bool
}

func (sink *captureSink) Write(data []byte) (int, error) {
	// Capture is authoritative. Passthrough sees bytes only after the collector
	// has accepted them, and its errors never reach the pipe-draining loop.
	written, err := sink.collector.Write(data)
	if err != nil {
		return written, err
	}
	if written != len(data) {
		return written, io.ErrShortWrite
	}
	if sink.passthrough == nil || sink.passthroughFailed.Load() {
		return len(data), nil
	}
	written, err = sink.passthrough.Write(data)
	if err != nil || written != len(data) {
		sink.passthroughFailed.Store(true)
		sink.passthrough = nil
	}
	return len(data), nil
}

func (sink *captureSink) abandonPassthrough() {
	if sink.passthroughConfigured {
		sink.passthroughFailed.Store(true)
	}
}

type drainResult struct {
	name     string
	complete bool
}

func attachCapturePipes(cmd *exec.Cmd, stdout, stderr *captureSink) (*capturePipes, error) {
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		stdoutReader.Close()
		stdoutWriter.Close()
		return nil, err
	}
	pipes := &capturePipes{
		stdoutReader: stdoutReader,
		stdoutWriter: stdoutWriter,
		stderrReader: stderrReader,
		stderrWriter: stderrWriter,
		stdout:       stdout,
		stderr:       stderr,
	}
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter
	return pipes, nil
}

func (pipes *capturePipes) closeWriters() {
	_ = pipes.stdoutWriter.Close()
	_ = pipes.stderrWriter.Close()
}

func (pipes *capturePipes) close() {
	_ = pipes.stdoutReader.Close()
	_ = pipes.stdoutWriter.Close()
	_ = pipes.stderrReader.Close()
	_ = pipes.stderrWriter.Close()
}

func (pipes *capturePipes) drain() <-chan drainResult {
	results := make(chan drainResult, 2)
	go drainPipe("stdout", pipes.stdoutReader, pipes.stdout, results)
	go drainPipe("stderr", pipes.stderrReader, pipes.stderr, results)
	return results
}

func drainPipe(name string, reader *os.File, sink *captureSink, results chan<- drainResult) {
	_, err := io.Copy(sink, reader)
	_ = reader.Close()
	results <- drainResult{name: name, complete: err == nil}
}

func waitForDrains(
	pipes *capturePipes,
	results <-chan drainResult,
	grace time.Duration,
	abandonOnDeadline bool,
) (bool, bool) {
	complete := map[string]bool{}
	remaining := 2
	timer := time.NewTimer(grace)
	defer timer.Stop()

	for remaining > 0 {
		select {
		case result := <-results:
			complete[result.name] = result.complete
			remaining--
		case <-timer.C:
			for {
				select {
				case result := <-results:
					complete[result.name] = result.complete
					remaining--
				default:
					if _, ok := complete["stdout"]; !ok {
						_ = pipes.stdoutReader.Close()
						if abandonOnDeadline {
							pipes.stdout.abandonPassthrough()
						}
					}
					if _, ok := complete["stderr"]; !ok {
						_ = pipes.stderrReader.Close()
						if abandonOnDeadline {
							pipes.stderr.abandonPassthrough()
						}
					}
					if abandonOnDeadline {
						return complete["stdout"], complete["stderr"]
					}
					for remaining > 0 {
						result := <-results
						complete[result.name] = result.complete
						remaining--
					}
					return complete["stdout"], complete["stderr"]
				}
			}
		}
	}
	return complete["stdout"], complete["stderr"]
}

func portableDirectory(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return abbreviateDirectory(path, home)
}

func abbreviateDirectory(path, home string) string {
	relative, err := filepath.Rel(home, path)
	if err != nil {
		return path
	}
	if relative == "." {
		return "~"
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.Join("~", relative)
}

func gitContext(ctx context.Context, dir string) *record.GitContext {
	commit, err := gitOutput(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return nil
	}
	branch, _ := gitOutput(ctx, dir, "branch", "--show-current")
	status, _ := gitOutput(ctx, dir, "status", "--porcelain")
	return &record.GitContext{
		Commit: commit,
		Branch: branch,
		Dirty:  strings.TrimSpace(status) != "",
	}
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
