package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/MorisKyiv/runprint/internal/capture"
	"github.com/MorisKyiv/runprint/internal/record"
	"github.com/MorisKyiv/runprint/internal/report"
)

const latestRecordPath = ".runprint/latest.json"

var version = record.DevelopmentVersion

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}

	switch args[0] {
	case "record":
		code, err := recordCommand(args[1:], stdout, stderr)
		if err != nil {
			fmt.Fprintln(stderr, "runprint:", report.EscapeInline(err.Error()))
			return code
		}
		return code
	case "check":
		code, err := checkCommand(args[1:], stdout)
		if err != nil {
			fmt.Fprintln(stderr, "runprint:", report.EscapeInline(err.Error()))
			return code
		}
		return code
	case "show":
		code, err := showCommand(args[1:], stdout)
		if err != nil {
			fmt.Fprintln(stderr, "runprint:", report.EscapeInline(err.Error()))
			return code
		}
		return code
	case "version":
		if len(args) != 1 {
			usage(stderr)
			return 2
		}
		fmt.Fprintf(stdout, "runprint %s\n", report.EscapeInline(effectiveVersion()))
		return 0
	case "help", "-h", "--help":
		if len(args) != 1 {
			usage(stderr)
			return 2
		}
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "runprint: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func recordCommand(args []string, stdout, stderr io.Writer) (int, error) {
	options, err := parseRecordOptions(args)
	if err != nil {
		return 2, err
	}

	interrupts := make(chan os.Signal, 2)
	signal.Notify(interrupts, capture.HandledSignals()...)
	defer signal.Stop(interrupts)
	restoreBrokenPipe := suppressBrokenPipe()
	defer restoreBrokenPipe()

	r, passthrough, err := capture.RunWithOutputAndPreflight(
		context.Background(),
		options.command,
		interrupts,
		stdout,
		stderr,
		effectiveVersion(),
		func() error { return preflightRecordDestination(options.path) },
	)
	if err != nil {
		var startErr *capture.StartError
		if errors.As(err, &startErr) {
			return startErr.ExitCode(), err
		}
		return 1, err
	}

	if err := prepareRecordDirectory(options.path, options.path == filepath.FromSlash(latestRecordPath)); err != nil {
		return 1, err
	}
	id, err := record.WriteFileWithContentID(options.path, r)
	if err != nil {
		return 1, err
	}

	if passthrough.StdoutFailed {
		fmt.Fprintln(stderr, "runprint: warning: live stdout stopped; record capture continued")
	}
	if passthrough.StderrFailed {
		fmt.Fprintln(stderr, "runprint: warning: live stderr stopped; record capture continued")
	}
	// Diagnostics are best effort and must not replace the command's status.
	fmt.Fprintf(stderr, "recorded %s (exit %d)\n", report.EscapeInline(options.path), r.ExitCode)
	fmt.Fprintf(stderr, "content id %s\n", id)
	return r.ExitCode, nil
}

type recordOptions struct {
	path    string
	command []string
}

func parseRecordOptions(args []string) (recordOptions, error) {
	options := recordOptions{path: filepath.FromSlash(latestRecordPath)}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			options.command = append([]string(nil), args[i+1:]...)
			i = len(args)
		case arg == "-o" || arg == "--output":
			if i+1 >= len(args) {
				return recordOptions{}, recordUsageError()
			}
			i++
			options.path = args[i]
		case strings.HasPrefix(arg, "--output="):
			options.path = strings.TrimPrefix(arg, "--output=")
		case strings.HasPrefix(arg, "-"):
			return recordOptions{}, fmt.Errorf("unknown record option %q", arg)
		default:
			options.command = append([]string(nil), args[i:]...)
			i = len(args)
		}
	}

	if options.path == "" || options.path == "-" || len(options.command) == 0 {
		return recordOptions{}, recordUsageError()
	}
	return options, nil
}

func recordUsageError() error {
	return fmt.Errorf("usage: runprint record [-o record.json] -- <command> [args...]")
}

func prepareRecordDirectory(path string, enforcePrivate bool) error {
	dir := filepath.Dir(path)
	_, err := os.Stat(dir)
	created := os.IsNotExist(err)
	if err != nil && !created {
		return err
	}
	if created {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	if created || enforcePrivate {
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func preflightRecordDestination(path string) error {
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return fmt.Errorf("record output %s is a directory", path)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	dir := filepath.Dir(path)
	for {
		info, err := os.Stat(dir)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("record output parent %s is not a directory", dir)
			}
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return err
		}
		dir = parent
	}

	temp, err := os.CreateTemp(dir, ".runprint-preflight-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Remove(tempPath); err != nil {
		return err
	}
	return nil
}

func checkCommand(args []string, stdout io.Writer) (int, error) {
	path, err := parseRecordPath("check", args)
	if err != nil {
		return 2, err
	}
	_, id, err := record.ReadFileWithContentID(path)
	if err != nil {
		return 1, fmt.Errorf("read %s: %w", path, err)
	}
	if _, err := fmt.Fprintln(stdout, id); err != nil {
		return 1, err
	}
	return 0, nil
}

func parseRecordPath(command string, args []string) (string, error) {
	path := filepath.FromSlash(latestRecordPath)
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) > 1 || (len(args) == 1 && strings.HasPrefix(args[0], "-") && args[0] != "-") {
		return "", fmt.Errorf("usage: runprint %s [record.json]", command)
	}
	if len(args) == 1 {
		path = args[0]
	}
	return path, nil
}

func showCommand(args []string, stdout io.Writer) (int, error) {
	options, err := parseShowOptions(args)
	if err != nil {
		return 2, err
	}

	r, id, err := record.ReadFileWithContentID(options.path)
	if err != nil {
		return 1, fmt.Errorf("read %s: %w", options.path, err)
	}

	if options.format == "markdown" {
		if err := report.WriteMarkdownWithContentID(stdout, r, id); err != nil {
			return 1, err
		}
		return 0, nil
	}

	if _, err := fmt.Fprintf(stdout, "record: %s\n", report.EscapeInline(options.path)); err != nil {
		return 1, err
	}
	if _, err := fmt.Fprintf(stdout, "content id: %s\n", id); err != nil {
		return 1, err
	}
	if options.raw {
		err = report.WriteRaw(stdout, r)
	} else {
		err = report.Write(stdout, r)
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

type showOptions struct {
	path   string
	format string
	raw    bool
}

func parseShowOptions(args []string) (showOptions, error) {
	options := showOptions{
		path:   filepath.FromSlash(latestRecordPath),
		format: "terminal",
	}
	pathSet := false
	positionalOnly := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !positionalOnly {
			switch {
			case arg == "--":
				positionalOnly = true
				continue
			case arg == "--raw":
				options.raw = true
				continue
			case arg == "--format":
				if i+1 >= len(args) {
					return showOptions{}, fmt.Errorf("usage: runprint show [--format terminal|markdown] [--raw] [record.json]")
				}
				i++
				options.format = args[i]
				continue
			case strings.HasPrefix(arg, "--format="):
				options.format = strings.TrimPrefix(arg, "--format=")
				continue
			case strings.HasPrefix(arg, "-"):
				return showOptions{}, fmt.Errorf("unknown show option %q", arg)
			}
		}

		if pathSet {
			return showOptions{}, fmt.Errorf("usage: runprint show [--format terminal|markdown] [--raw] [record.json]")
		}
		options.path = arg
		pathSet = true
	}

	if options.format != "terminal" && options.format != "markdown" {
		return showOptions{}, fmt.Errorf("unknown show format %q", options.format)
	}
	if options.raw && options.format != "terminal" {
		return showOptions{}, fmt.Errorf("--raw is only valid with terminal format")
	}
	return options, nil
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage:")
	fmt.Fprintln(w, "  runprint record [-o record.json] -- <command> [args...]")
	fmt.Fprintln(w, "  runprint check [record.json]")
	fmt.Fprintln(w, "  runprint show [--format terminal|markdown] [--raw] [record.json]")
	fmt.Fprintln(w, "  runprint version")
}

func effectiveVersion() string {
	moduleVersion := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		moduleVersion = info.Main.Version
	}
	return selectVersion(version, moduleVersion)
}

func selectVersion(linkedVersion, moduleVersion string) string {
	if linkedVersion != "" && linkedVersion != record.DevelopmentVersion {
		return linkedVersion
	}
	if moduleVersion != "" && moduleVersion != "(devel)" {
		return moduleVersion
	}
	return record.DevelopmentVersion
}
