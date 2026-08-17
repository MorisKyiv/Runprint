//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MorisKyiv/runprint/internal/record"
)

const interruptHelperEnvironment = "RUNPRINT_INTERRUPT_HELPER"
const interruptTargetMarker = "runprint-record-interrupt-target"

func TestRecordCommandWritesArtifactAfterInterrupt(t *testing.T) {
	dir := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestRecordCommandInterruptHelper$")
	command.Dir = dir
	command.Env = append(os.Environ(), interruptHelperEnvironment+"=1")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waitForHelperFile(t, filepath.Join(dir, "child-ready"))
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}

	err := command.Wait()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 130 {
		t.Fatalf("helper error = %v, want exit status 130", err)
	}

	path := filepath.Join(dir, filepath.FromSlash(latestRecordPath))
	r, err := record.ReadFile(path)
	if err != nil {
		t.Fatalf("read interrupted record: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if r.ExitCode != 130 {
		t.Fatalf("recorded exit code = %d, want 130", r.ExitCode)
	}
	if r.Interruption == nil || r.Interruption.Signal != "SIGINT" {
		t.Fatalf("recorded interruption = %#v, want SIGINT", r.Interruption)
	}
	if r.RunprintVersion != record.DevelopmentVersion {
		t.Fatalf("runprint version = %q, want %q", r.RunprintVersion, record.DevelopmentVersion)
	}
	if r.Stdout.HeadText != "started" {
		t.Fatalf("recorded stdout = %q, want started", r.Stdout.HeadText)
	}
	if !strings.Contains(r.Stderr.HeadText, "interrupted") {
		t.Fatalf("recorded stderr = %q, want interrupt output", r.Stderr.HeadText)
	}
	if !r.Stdout.CaptureComplete || !r.Stderr.CaptureComplete {
		t.Fatal("interrupted command streams were not fully drained")
	}
	if stdout.String() != "started" {
		t.Fatalf("helper stdout = %q, want exact child stdout", stdout.String())
	}
	if !strings.Contains(stderr.String(), "recorded .runprint/latest.json (exit 130)") {
		t.Fatalf("helper stderr = %q, want saved-record confirmation", stderr.String())
	}
}

func TestRecordCommandCapturesAfterBrokenStdoutPipe(t *testing.T) {
	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	var stderr bytes.Buffer
	code, err := recordCommand([]string{
		"--", "sh", "-c",
		"dd if=/dev/zero bs=1024 count=300 2>/dev/null | tr '\\000' x",
	}, writer, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	r, err := record.ReadFile(filepath.FromSlash(latestRecordPath))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := int64(r.Stdout.ReceivedBytes), int64(300<<10); got != want {
		t.Fatalf("captured stdout bytes = %d, want %d", got, want)
	}
	if !r.Stdout.CaptureComplete {
		t.Fatal("stdout capture stopped after live output pipe broke")
	}
	if !strings.Contains(stderr.String(), "live stdout stopped; record capture continued") {
		t.Fatalf("stderr = %q, want safe passthrough warning", stderr.String())
	}
	if !strings.Contains(stderr.String(), "recorded .runprint/latest.json (exit 0)") {
		t.Fatalf("stderr = %q, want record confirmation", stderr.String())
	}
}

func TestRecordCommandInterruptHelper(t *testing.T) {
	if os.Getenv(interruptHelperEnvironment) != "1" {
		return
	}
	ready, err := filepath.Abs("child-ready")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(98)
	}
	code, err := recordCommand([]string{
		"--", os.Args[0],
		"-test.run=^TestRecordCommandInterruptTargetHelper$",
		"--", interruptTargetMarker, ready,
	}, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(99)
	}
	os.Exit(code)
}

func TestRecordCommandInterruptTargetHelper(t *testing.T) {
	ready, ok := interruptTargetArgument()
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

func waitForHelperFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for helper child; expected %s", path)
}

func interruptTargetArgument() (string, bool) {
	for index := 0; index+2 < len(os.Args); index++ {
		if os.Args[index] == "--" && os.Args[index+1] == interruptTargetMarker {
			return os.Args[index+2], true
		}
	}
	return "", false
}
