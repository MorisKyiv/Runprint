package capture

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MorisKyiv/runprint/internal/record"
)

func TestRunCapturesSuccessfulCommand(t *testing.T) {
	var argv []string
	if runtime.GOOS == "windows" {
		argv = []string{"cmd", "/c", "echo", "hello"}
	} else {
		argv = []string{"sh", "-c", "printf hello"}
	}

	r, err := Run(context.Background(), argv)
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", r.ExitCode)
	}
	if r.Stdout.HeadText == "" {
		t.Fatal("stdout was not captured")
	}
	if r.Version != record.CurrentVersion {
		t.Fatalf("version = %d, want %d", r.Version, record.CurrentVersion)
	}
	if r.RunprintVersion != record.DevelopmentVersion {
		t.Fatalf("runprint version = %q, want %q", r.RunprintVersion, record.DevelopmentVersion)
	}
}

func TestRunBoundsLargeCommandOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is POSIX-specific")
	}
	argv := []string{"sh", "-c", "dd if=/dev/zero bs=1024 count=300 2>/dev/null | tr '\\000' x"}
	r, err := Run(context.Background(), argv)
	if err != nil {
		t.Fatal(err)
	}
	if r.Stdout.ReceivedBytes != 300<<10 {
		t.Fatalf("received bytes = %d, want %d", r.Stdout.ReceivedBytes, 300<<10)
	}
	if r.Stdout.OmittedBytes == 0 {
		t.Fatal("large output was not truncated")
	}
	if int64(r.Stdout.HeadBytes+r.Stdout.TailBytes) > record.MaxCapturedBytes {
		t.Fatalf("retained bytes = %d, want at most %d", r.Stdout.HeadBytes+r.Stdout.TailBytes, record.MaxCapturedBytes)
	}
	if !r.Stdout.CaptureComplete {
		t.Fatal("completed command was marked as incomplete capture")
	}
}

func TestRunRejectsEmptyCommand(t *testing.T) {
	if _, err := Run(context.Background(), nil); err == nil {
		t.Fatal("expected an error")
	}
}

func TestRunRejectsOversizedMetadataBeforeStartingCommand(t *testing.T) {
	argument := strings.Repeat("<", record.MaxCommandJSONBytes/5)
	_, err := Run(context.Background(), []string{"definitely-not-started", argument})
	if err == nil || !strings.Contains(err.Error(), "record metadata") {
		t.Fatalf("error = %v, want preflight metadata rejection", err)
	}
}

func TestWaitForDrainsMarksStreamWithoutEOFIncomplete(t *testing.T) {
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		stdoutReader.Close()
		stdoutWriter.Close()
		t.Fatal(err)
	}
	pipes := &capturePipes{
		stdoutReader: stdoutReader,
		stdoutWriter: stdoutWriter,
		stderrReader: stderrReader,
		stderrWriter: stderrWriter,
		stdout: &captureSink{
			collector: newStreamCollector(defaultHeadBytes, defaultTailBytes),
		},
		stderr: &captureSink{
			collector: newStreamCollector(defaultHeadBytes, defaultTailBytes),
		},
	}
	t.Cleanup(pipes.close)

	if _, err := stdoutWriter.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	if err := stderrWriter.Close(); err != nil {
		t.Fatal(err)
	}

	stdoutComplete, stderrComplete := waitForDrains(pipes, pipes.drain(), 20*time.Millisecond, false)
	if stdoutComplete {
		t.Fatal("stdout capture was marked complete without EOF")
	}
	if !stderrComplete {
		t.Fatal("stderr capture was marked incomplete after EOF")
	}
	if got := string(pipes.stdout.collector.snapshot().head); got != "partial" {
		t.Fatalf("stdout = %q, want partial", got)
	}
}

func TestWaitForCommandPrefersKnownReapToLateSignal(t *testing.T) {
	waitResult := make(chan error, 1)
	waitResult <- nil
	interrupts := make(chan os.Signal, 1)
	interrupts <- os.Interrupt
	var reaped atomic.Bool
	reaped.Store(true)

	err, interrupted, cancelled, _ := waitForCommand(
		context.Background(),
		exec.Command("not-started"),
		waitResult,
		&reaped,
		interrupts,
		true,
		time.Second,
	)
	if err != nil || interrupted != nil || cancelled {
		t.Fatalf("result = (%v, %v, %v), want a clean command result", err, interrupted, cancelled)
	}
}

func TestAbbreviateDirectoryRemovesHomePrefixOnly(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "home", "alice")
	inside := filepath.Join(home, "work", "runprint")
	outside := filepath.Join(string(filepath.Separator), "srv", "runprint")

	if got, want := abbreviateDirectory(inside, home), filepath.Join("~", "work", "runprint"); got != want {
		t.Fatalf("inside-home path = %q, want %q", got, want)
	}
	if got := abbreviateDirectory(home, home); got != "~" {
		t.Fatalf("home path = %q, want ~", got)
	}
	if got := abbreviateDirectory(outside, home); got != outside {
		t.Fatalf("outside-home path = %q, want %q", got, outside)
	}
}

func TestRunCapturesGitContextBeforeCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "Runprint Test")
	runGit(t, dir, "config", "user.email", "runprint@example.invalid")
	runGit(t, dir, "config", "core.autocrlf", "false")
	tracked := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "tracked.txt")
	runGit(t, dir, "commit", "-m", "initial")
	wantCommit := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))

	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	argv := []string{"sh", "-c", "printf changed > tracked.txt"}
	if runtime.GOOS == "windows" {
		argv = []string{"cmd", "/c", "echo changed>tracked.txt"}
	}
	r, _, err := RunWithOutputAndPreflight(
		context.Background(),
		argv,
		nil,
		nil,
		nil,
		record.DevelopmentVersion,
		func() error {
			return os.Mkdir(filepath.Join(dir, ".runprint"), 0o700)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.Git == nil {
		t.Fatal("Git context was not captured")
	}
	if r.Git.Commit != wantCommit {
		t.Fatalf("Git commit = %q, want %q", r.Git.Commit, wantCommit)
	}
	if r.Git.Dirty {
		t.Fatal("Git context reports preflight or command-created changes as pre-existing")
	}
	contents, err := os.ReadFile(tracked)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "changed") {
		t.Fatalf("command did not modify tracked file: %q", contents)
	}
}

func TestPreflightFailurePreventsCommandStart(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "command-ran")
	argv := []string{"sh", "-c", "printf ran > command-ran"}
	if runtime.GOOS == "windows" {
		argv = []string{"cmd", "/c", "echo ran>command-ran"}
	}

	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	_, _, err = RunWithOutputAndPreflight(
		context.Background(),
		argv,
		nil,
		nil,
		nil,
		record.DevelopmentVersion,
		func() error { return errors.New("destination unavailable") },
	)
	if err == nil || !strings.Contains(err.Error(), "destination unavailable") {
		t.Fatalf("error = %v, want preflight failure", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("command marker exists after preflight failure; stat error = %v", err)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
