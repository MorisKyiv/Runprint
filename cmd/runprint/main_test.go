package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MorisKyiv/runprint/internal/record"
)

func TestRunShowRendersRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "failure.json")
	want := record.Record{
		Version:         record.CurrentVersion,
		RunprintVersion: record.DevelopmentVersion,
		Command:         []string{"go", "test", "./..."},
		Directory:       "/workspace/runprint",
		StartedAt:       time.Date(2026, 8, 17, 12, 30, 0, 0, time.UTC),
		Duration:        1500 * time.Millisecond,
		ExitCode:        1,
		Stdout:          record.NewStream(15, true, []byte("package output\n"), 0, nil),
		Stderr:          record.NewStream(12, true, []byte("test failed\n"), 0, nil),
		Git: &record.GitContext{
			Commit: "abc123",
			Branch: "main",
			Dirty:  true,
		},
	}
	if err := record.WriteFile(path, want); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"show", path}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
	for _, fragment := range []string{
		"record: " + path,
		"content id: sha256:",
		`command: ["go","test","./..."]`,
		"exit code: 1",
		"git branch: main",
		"stdout:\n| package output",
		"stderr:\n| test failed",
	} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Errorf("output does not contain %q:\n%s", fragment, stdout.String())
		}
	}
}

func TestRunShowReportsInvalidRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(path, []byte("{not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"show", path}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "decode record") {
		t.Fatalf("stderr = %q, want a decode error", stderr.String())
	}
}

func TestRunShowEscapesControlsInErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing\x1b[2J\nforged.json")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"show", path}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if strings.Contains(stderr.String(), "\x1b") {
		t.Fatalf("stderr contains a raw escape character: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), `\x1b[2J`) {
		t.Fatalf("stderr = %q, want a visible escape", stderr.String())
	}
	if strings.Contains(stderr.String(), "\nforged.json") {
		t.Fatalf("stderr contains an injected line: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), `\x0aforged.json`) {
		t.Fatalf("stderr = %q, want a visible newline escape", stderr.String())
	}
}

func TestRunShowRendersPortableMarkdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "failure.json")
	want := record.Record{
		Version:         record.CurrentVersion,
		RunprintVersion: record.DevelopmentVersion,
		Command:         []string{"go", "test", "./..."},
		Directory:       "/home/alice/work/runprint",
		StartedAt:       time.Date(2026, 8, 17, 12, 30, 0, 0, time.UTC),
		Duration:        1500 * time.Millisecond,
		ExitCode:        1,
		Stdout:          record.NewStream(15, true, []byte("package output\n"), 0, nil),
		Stderr:          record.NewStream(12, true, []byte("test failed\n"), 0, nil),
	}
	if err := record.WriteFile(path, want); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"show", "--format=markdown", path}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
	for _, fragment := range []string{
		"## Runprint execution report",
		"**Content ID:** `sha256:",
		"~/work/runprint",
		"### stdout",
		"package output",
		"Runprint does not scan for secrets",
	} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Errorf("output does not contain %q:\n%s", fragment, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "record: "+path) {
		t.Fatalf("portable Markdown contains local record path:\n%s", stdout.String())
	}
}

func TestParseShowOptionsRejectsUnsafeMarkdownCombination(t *testing.T) {
	_, err := parseShowOptions([]string{"--format", "markdown", "--raw"})
	if err == nil || !strings.Contains(err.Error(), "only valid with terminal") {
		t.Fatalf("error = %v, want incompatible-option error", err)
	}
}

func TestRunRecordPreservesCommandExitCode(t *testing.T) {
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

	command := []string{"sh", "-c", "printf output; printf failure >&2; exit 7"}
	if runtime.GOOS == "windows" {
		command = []string{"cmd", "/c", "echo output & echo failure 1>&2 & exit /b 7"}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	args := append([]string{"record", "--"}, command...)
	code := run(args, &stdout, &stderr)
	if code != 7 {
		t.Fatalf("exit code = %d, want 7; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "output") {
		t.Fatalf("live stdout = %q, want command output", stdout.String())
	}
	if strings.Contains(stdout.String(), "recorded ") {
		t.Fatalf("stdout contains Runprint diagnostic: %q", stdout.String())
	}
	confirmation := "recorded " + filepath.FromSlash(latestRecordPath) + " (exit 7)"
	if !strings.Contains(stderr.String(), "failure") ||
		!strings.Contains(stderr.String(), confirmation) {
		t.Fatalf("live stderr = %q, want command output and record confirmation", stderr.String())
	}
	if !strings.Contains(stderr.String(), "content id sha256:") {
		t.Fatalf("stderr = %q, want content ID", stderr.String())
	}

	r, err := record.ReadFile(filepath.FromSlash(latestRecordPath))
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 7 {
		t.Fatalf("recorded exit code = %d, want 7", r.ExitCode)
	}
	if !strings.Contains(r.Stderr.HeadText, "failure") {
		t.Fatalf("recorded stderr = %q, want failure output", r.Stderr.HeadText)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Dir(filepath.FromSlash(latestRecordPath)))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("record directory mode = %o, want 700", got)
		}
	}
}

func TestRunRecordReturns127AndWritesNoRecordWhenCommandIsMissing(t *testing.T) {
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

	const missing = "runprint-command-that-does-not-exist-8d39cf53"
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"record", "--", missing}, &stdout, &stderr); code != 127 {
		t.Fatalf("exit code = %d, want 127; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), missing) || !strings.Contains(stderr.String(), "start command") {
		t.Fatalf("stderr = %q, want an explicit start error", stderr.String())
	}
	if _, err := os.Stat(filepath.FromSlash(latestRecordPath)); !os.IsNotExist(err) {
		t.Fatalf("record exists for a command that never started; stat error = %v", err)
	}
}

func TestRunRecordReturns126AndWritesNoRecordWhenCommandCannotExecute(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix execute permissions are not available")
	}
	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	workingDirectory := t.TempDir()
	if err := os.Chdir(workingDirectory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	command := filepath.Join(workingDirectory, "not-executable")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"record", "--", command}, &stdout, &stderr); code != 126 {
		t.Fatalf("exit code = %d, want 126; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "start command") {
		t.Fatalf("stderr = %q, want an explicit start error", stderr.String())
	}
	if _, err := os.Stat(filepath.FromSlash(latestRecordPath)); !os.IsNotExist(err) {
		t.Fatalf("record exists for a command that never started; stat error = %v", err)
	}
}

func TestRunRecordWritesSelectedOutput(t *testing.T) {
	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	workingDirectory := t.TempDir()
	if err := os.Chdir(workingDirectory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	outputPath := filepath.Join(workingDirectory, "artifacts", "failure.json")
	command := []string{"sh", "-c", "printf selected; exit 3"}
	if runtime.GOOS == "windows" {
		command = []string{"cmd", "/c", "echo selected & exit /b 3"}
	}
	args := append([]string{"record", "--output", outputPath, "--"}, command...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != 3 {
		t.Fatalf("exit code = %d, want 3; stderr = %q", code, stderr.String())
	}
	if _, err := record.ReadFile(outputPath); err != nil {
		t.Fatalf("read selected output: %v", err)
	}
	if _, err := os.Stat(filepath.FromSlash(latestRecordPath)); !os.IsNotExist(err) {
		t.Fatalf("default record unexpectedly exists; stat error = %v", err)
	}
}

func TestRunRecordRejectsInvalidOutputBeforeCommandStarts(t *testing.T) {
	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	workingDirectory := t.TempDir()
	if err := os.Chdir(workingDirectory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	blockedParent := filepath.Join(workingDirectory, "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workingDirectory, "command-ran")
	command := []string{"sh", "-c", "printf ran > command-ran"}
	if runtime.GOOS == "windows" {
		command = []string{"cmd", "/c", "echo ran>command-ran"}
	}
	args := append([]string{"record", "--output", filepath.Join(blockedParent, "record.json"), "--"}, command...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("command marker exists after output preflight failure; stat error = %v", err)
	}
}

func TestPreflightRecordDestinationLeavesMissingParentAbsent(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "missing", "nested")
	if err := preflightRecordDestination(filepath.Join(parent, "record.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Fatalf("preflight created the record parent; stat error = %v", err)
	}
}

func TestRunRecordDoesNotExposeReceiptDirectoryToCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	workingDirectory := t.TempDir()
	runMainGit(t, workingDirectory, "init")
	runMainGit(t, workingDirectory, "config", "user.name", "Runprint Test")
	runMainGit(t, workingDirectory, "config", "user.email", "runprint@example.invalid")
	runMainGit(t, workingDirectory, "commit", "--allow-empty", "-m", "initial")
	if err := os.Chdir(workingDirectory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"record", "--", "git", "status", "--porcelain"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("git status observed Runprint preflight state: %q", stdout.String())
	}
	r, err := record.ReadFile(filepath.FromSlash(latestRecordPath))
	if err != nil {
		t.Fatal(err)
	}
	if r.Stdout.ReceivedBytes != 0 {
		t.Fatalf("recorded git status = %q, want clean output", r.Stdout.HeadText)
	}
}

func TestRunCheckValidatesAndIdentifiesRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "failure.json")
	if err := record.WriteFile(path, record.Record{
		Version:         record.CurrentVersion,
		RunprintVersion: record.DevelopmentVersion,
		Command:         []string{"false"},
		Directory:       "/tmp/project",
		StartedAt:       time.Date(2026, 8, 17, 12, 30, 0, 0, time.UTC),
		ExitCode:        1,
		Stdout:          record.NewStream(0, true, nil, 0, nil),
		Stderr:          record.NewStream(0, true, nil, 0, nil),
	}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"check", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
	id := strings.TrimSpace(stdout.String())
	if !strings.HasPrefix(id, record.ContentIDPrefix) || len(id) != len(record.ContentIDPrefix)+64 {
		t.Fatalf("content ID = %q, want sha256 plus 64 hex digits", id)
	}
}

func TestParseRecordOptions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		path    string
		command []string
		wantErr bool
	}{
		{name: "default", args: []string{"--", "go", "test"}, path: filepath.FromSlash(latestRecordPath), command: []string{"go", "test"}},
		{name: "short output", args: []string{"-o", "failure.json", "--", "false"}, path: "failure.json", command: []string{"false"}},
		{name: "long output", args: []string{"--output=artifact.json", "go", "test"}, path: "artifact.json", command: []string{"go", "test"}},
		{name: "missing command", args: []string{"--output", "artifact.json", "--"}, wantErr: true},
		{name: "stdout path", args: []string{"--output=-", "--", "false"}, wantErr: true},
		{name: "unknown option", args: []string{"--wat", "false"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseRecordOptions(test.args)
			if test.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.path != test.path || strings.Join(got.command, "\x00") != strings.Join(test.command, "\x00") {
				t.Fatalf("options = %#v, want path %q command %q", got, test.path, test.command)
			}
		})
	}
}

func TestRunWithoutCommandReturnsUsageError(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(nil, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "runprint show") {
		t.Fatalf("stderr = %q, want full usage", stderr.String())
	}
}

func TestRunVersionUsesInjectedBuildVersion(t *testing.T) {
	previous := version
	version = "v0.1.0-test"
	t.Cleanup(func() { version = previous })

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"version"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "runprint v0.1.0-test\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestSelectVersionUsesModuleVersionForGoInstall(t *testing.T) {
	tests := []struct {
		name     string
		linked   string
		module   string
		expected string
	}{
		{name: "linked release", linked: "v1.0.0", module: "v0.9.0", expected: "v1.0.0"},
		{name: "go install", linked: record.DevelopmentVersion, module: "v1.0.0", expected: "v1.0.0"},
		{name: "local build", linked: record.DevelopmentVersion, module: "(devel)", expected: record.DevelopmentVersion},
		{name: "empty metadata", expected: record.DevelopmentVersion},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := selectVersion(test.linked, test.module); got != test.expected {
				t.Fatalf("version = %q, want %q", got, test.expected)
			}
		})
	}
}

func runMainGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
