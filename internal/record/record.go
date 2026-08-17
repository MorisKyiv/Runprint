package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"
)

const (
	LegacyVersion          = 1
	CurrentVersion         = 2
	DevelopmentVersion     = "dev"
	MaxRecordBytes         = 8 << 20
	MaxCapturedBytes       = 256 << 10
	MaxCommandJSONBytes    = 2 << 20
	maxMetadataStringBytes = 64 << 10
	maxProducerVersion     = 128
	maxSignalName          = 32
)

// Record is a portable description of one command execution.
type Record struct {
	Version         int           `json:"version"`
	RunprintVersion string        `json:"runprint_version,omitempty"`
	Command         []string      `json:"command"`
	Directory       string        `json:"directory"`
	StartedAt       time.Time     `json:"started_at"`
	Duration        time.Duration `json:"duration_ns"`
	ExitCode        int           `json:"exit_code"`
	Interruption    *Interruption `json:"interruption,omitempty"`
	Stdout          Stream        `json:"stdout"`
	Stderr          Stream        `json:"stderr"`
	Git             *GitContext   `json:"git,omitempty"`
}

// Interruption records a termination signal received and acted on by
// Runprint. Presence distinguishes an interrupted recording from a command
// that independently chose the same conventional numeric exit code.
type Interruption struct {
	Signal string `json:"signal"`
}

type GitContext struct {
	Commit string `json:"commit,omitempty"`
	Branch string `json:"branch,omitempty"`
	Dirty  bool   `json:"dirty"`
}

// Decode reads and validates exactly one bounded execution record. Schema v1
// remains readable, but all newly written records use schema v2.
func Decode(input io.Reader) (Record, error) {
	r, _, err := DecodeWithContentID(input)
	return r, err
}

// DecodeWithContentID reads and validates exactly one bounded execution
// record and returns a SHA-256 identifier for the exact artifact bytes read.
// The identifier detects byte changes but does not authenticate the record.
func DecodeWithContentID(input io.Reader) (Record, string, error) {
	data, err := readBounded(input)
	if err != nil {
		return Record{}, "", fmt.Errorf("decode record: %w", err)
	}
	if err := validateStrictJSON(data); err != nil {
		return Record{}, "", fmt.Errorf("decode record: %w", err)
	}

	var envelope struct {
		Version *int `json:"version"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Record{}, "", fmt.Errorf("decode record: %w", err)
	}
	if envelope.Version == nil {
		return Record{}, "", errors.New("decode record: record version is missing")
	}

	var r Record
	switch *envelope.Version {
	case LegacyVersion:
		r, err = decodeLegacy(data)
	case CurrentVersion:
		r, err = decodeV2(data)
	default:
		return Record{}, "", fmt.Errorf("unsupported record version %d", *envelope.Version)
	}
	if err != nil {
		return Record{}, "", fmt.Errorf("decode record: %w", err)
	}
	if err := r.Validate(); err != nil {
		return Record{}, "", err
	}
	return r, contentID(data), nil
}

func readBounded(input io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(input, MaxRecordBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxRecordBytes {
		return nil, fmt.Errorf("record exceeds %d-byte limit", MaxRecordBytes)
	}
	return data, nil
}

type legacyRecord struct {
	Version   int           `json:"version"`
	Command   []string      `json:"command"`
	Directory string        `json:"directory"`
	StartedAt time.Time     `json:"started_at"`
	Duration  time.Duration `json:"duration_ns"`
	ExitCode  int           `json:"exit_code"`
	Stdout    string        `json:"stdout"`
	Stderr    string        `json:"stderr"`
	Git       *GitContext   `json:"git,omitempty"`
}

func decodeLegacy(data []byte) (Record, error) {
	var legacy legacyRecord
	if err := json.Unmarshal(data, &legacy); err != nil {
		return Record{}, err
	}
	return Record{
		Version:   legacy.Version,
		Command:   legacy.Command,
		Directory: legacy.Directory,
		StartedAt: legacy.StartedAt,
		Duration:  legacy.Duration,
		ExitCode:  legacy.ExitCode,
		Stdout:    NewStream(int64(len(legacy.Stdout)), true, []byte(legacy.Stdout), 0, nil),
		Stderr:    NewStream(int64(len(legacy.Stderr)), true, []byte(legacy.Stderr), 0, nil),
		Git:       legacy.Git,
	}, nil
}

type recordV2Wire struct {
	Version         *int                `json:"version"`
	RunprintVersion *string             `json:"runprint_version,omitempty"`
	Command         *[]*string          `json:"command"`
	Directory       *string             `json:"directory"`
	StartedAt       *time.Time          `json:"started_at"`
	Duration        *time.Duration      `json:"duration_ns"`
	ExitCode        *int                `json:"exit_code"`
	Interruption    *interruptionV2Wire `json:"interruption,omitempty"`
	Stdout          *streamV2Wire       `json:"stdout"`
	Stderr          *streamV2Wire       `json:"stderr"`
	Git             *gitContextV2Wire   `json:"git,omitempty"`
}

type interruptionV2Wire struct {
	Signal *string `json:"signal"`
}

type gitContextV2Wire struct {
	Commit *string `json:"commit,omitempty"`
	Branch *string `json:"branch,omitempty"`
	Dirty  *bool   `json:"dirty"`
}

func decodeV2(data []byte) (Record, error) {
	var wire recordV2Wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return Record{}, err
	}
	if wire.Version == nil || wire.Command == nil || wire.Directory == nil ||
		wire.StartedAt == nil || wire.Duration == nil || wire.ExitCode == nil ||
		wire.Stdout == nil || wire.Stderr == nil {
		return Record{}, errors.New("schema v2 record is missing a required field")
	}
	command := make([]string, len(*wire.Command))
	for index, argument := range *wire.Command {
		if argument == nil {
			return Record{}, fmt.Errorf("command argument %d is null", index)
		}
		command[index] = *argument
	}

	stdout, err := wire.Stdout.stream("stdout")
	if err != nil {
		return Record{}, err
	}
	stderr, err := wire.Stderr.stream("stderr")
	if err != nil {
		return Record{}, err
	}
	var git *GitContext
	if wire.Git != nil {
		if wire.Git.Dirty == nil {
			return Record{}, errors.New("git.dirty is missing")
		}
		git = &GitContext{Dirty: *wire.Git.Dirty}
		if wire.Git.Commit != nil {
			git.Commit = *wire.Git.Commit
		}
		if wire.Git.Branch != nil {
			git.Branch = *wire.Git.Branch
		}
	}
	var interruption *Interruption
	if wire.Interruption != nil {
		if wire.Interruption.Signal == nil {
			return Record{}, errors.New("interruption.signal is missing")
		}
		interruption = &Interruption{Signal: *wire.Interruption.Signal}
	}

	r := Record{
		Version:      *wire.Version,
		Command:      command,
		Directory:    *wire.Directory,
		StartedAt:    *wire.StartedAt,
		Duration:     *wire.Duration,
		ExitCode:     *wire.ExitCode,
		Interruption: interruption,
		Stdout:       stdout,
		Stderr:       stderr,
		Git:          git,
	}
	if wire.RunprintVersion != nil {
		r.RunprintVersion = *wire.RunprintVersion
	}
	return r, nil
}

// ReadFile reads and validates an execution record from path.
func ReadFile(path string) (Record, error) {
	r, _, err := ReadFileWithContentID(path)
	return r, err
}

// ReadFileWithContentID reads and validates an execution record and returns
// the SHA-256 identifier of the exact file bytes.
func ReadFileWithContentID(path string) (Record, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return Record{}, "", err
	}
	defer file.Close()
	return DecodeWithContentID(file)
}

// WriteFile atomically replaces path with a validated schema v2 record.
func WriteFile(path string, r Record) error {
	_, err := WriteFileWithContentID(path, r)
	return err
}

// WriteFileWithContentID atomically replaces path with a validated schema v2
// record and returns the SHA-256 identifier of the exact bytes written.
func WriteFileWithContentID(path string, r Record) (string, error) {
	if r.Version != CurrentVersion {
		return "", fmt.Errorf("cannot write record version %d (want %d)", r.Version, CurrentVersion)
	}
	if err := r.Validate(); err != nil {
		return "", err
	}
	if r.RunprintVersion == "" {
		return "", errors.New("cannot write schema v2 record without runprint_version")
	}

	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode record: %w", err)
	}
	data = append(data, '\n')
	if len(data) > MaxRecordBytes {
		return "", fmt.Errorf("encoded record exceeds %d-byte limit", MaxRecordBytes)
	}
	if _, err := Decode(bytes.NewReader(data)); err != nil {
		return "", fmt.Errorf("encoded record failed strict self-validation: %w", err)
	}
	id := contentID(data)

	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".runprint-*.tmp")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return "", err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return "", err
	}
	return id, nil
}

// Validate checks the invariants required by supported record schemas.
func (r Record) Validate() error {
	if r.Version != LegacyVersion && r.Version != CurrentVersion {
		return fmt.Errorf("unsupported record version %d", r.Version)
	}
	if len(r.Command) == 0 {
		return errors.New("record command is empty")
	}
	commandJSON, err := json.Marshal(r.Command)
	if err != nil {
		return fmt.Errorf("encode record command: %w", err)
	}
	if len(commandJSON) > MaxCommandJSONBytes {
		return fmt.Errorf("record command exceeds %d-byte encoded limit", MaxCommandJSONBytes)
	}
	for index, argument := range r.Command {
		if !utf8.ValidString(argument) {
			return fmt.Errorf("record command argument %d is not valid UTF-8", index)
		}
	}
	if r.StartedAt.IsZero() {
		return errors.New("record start time is missing")
	}
	if r.Duration < 0 {
		return errors.New("record duration is negative")
	}
	if r.Version == CurrentVersion {
		if r.Directory == "" {
			return errors.New("record directory is empty")
		}
		if err := validateMetadataString("record directory", r.Directory, maxMetadataStringBytes); err != nil {
			return err
		}
		if r.RunprintVersion != "" {
			if err := validateMetadataString("runprint version", r.RunprintVersion, maxProducerVersion); err != nil {
				return err
			}
		}
		if r.Interruption != nil {
			expectedExitCode, err := validateSignalName(r.Interruption.Signal)
			if err != nil {
				return err
			}
			if r.ExitCode != expectedExitCode {
				return fmt.Errorf(
					"interruption signal %s requires exit code %d",
					r.Interruption.Signal,
					expectedExitCode,
				)
			}
		}
		if r.Git != nil {
			if err := validateMetadataString("git commit", r.Git.Commit, maxMetadataStringBytes); err != nil {
				return err
			}
			if err := validateMetadataString("git branch", r.Git.Branch, maxMetadataStringBytes); err != nil {
				return err
			}
		}
		if err := r.Stdout.Validate("stdout"); err != nil {
			return err
		}
		if err := r.Stderr.Validate("stderr"); err != nil {
			return err
		}
	}
	return nil
}

func validateMetadataString(name, value string, limit int) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s is not valid UTF-8", name)
	}
	if len(value) > limit {
		return fmt.Errorf("%s exceeds %d-byte limit", name, limit)
	}
	return nil
}

func validateSignalName(value string) (int, error) {
	if value == "" {
		return 0, errors.New("interruption signal is empty")
	}
	if err := validateMetadataString("interruption signal", value, maxSignalName); err != nil {
		return 0, err
	}
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return 0, fmt.Errorf("interruption signal %q is not canonical", value)
	}
	switch value {
	case "SIGHUP":
		return 129, nil
	case "SIGINT", "interrupt":
		return 130, nil
	case "SIGQUIT":
		return 131, nil
	case "SIGTERM":
		return 143, nil
	default:
		return 0, fmt.Errorf("interruption signal %q is not supported", value)
	}
}
