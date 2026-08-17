package record

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDecodeRoundTripV2(t *testing.T) {
	want := validRecord()
	data := marshalRecord(t, want)

	got, err := Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded record = %#v, want %#v", got, want)
	}
}

func TestDecodeWithContentIDHashesExactArtifactBytes(t *testing.T) {
	data := append(marshalRecord(t, validRecord()), '\n')
	_, id, err := DecodeWithContentID(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	want := ContentIDPrefix + hex.EncodeToString(digest[:])
	if id != want {
		t.Fatalf("content ID = %q, want %q", id, want)
	}

	reformatted := append([]byte{' '}, data...)
	_, reformattedID, err := DecodeWithContentID(bytes.NewReader(reformatted))
	if err != nil {
		t.Fatal(err)
	}
	if reformattedID == id {
		t.Fatal("byte-distinct artifacts received the same content ID")
	}
}

func TestDecodeReadsPreReleaseV2WithoutProducerVersion(t *testing.T) {
	data := string(marshalRecord(t, validRecord()))
	data = strings.Replace(data, `"runprint_version":"dev",`, "", 1)

	got, err := Decode(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if got.RunprintVersion != "" {
		t.Fatalf("runprint version = %q, want unknown pre-release producer", got.RunprintVersion)
	}
}

func TestDecodePreservesExplicitInterruption(t *testing.T) {
	want := validRecord()
	want.ExitCode = 130
	want.Interruption = &Interruption{Signal: "SIGINT"}

	got, err := Decode(bytes.NewReader(marshalRecord(t, want)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Interruption, want.Interruption) {
		t.Fatalf("interruption = %#v, want %#v", got.Interruption, want.Interruption)
	}
}

func TestV2PreservesInvalidUTF8Bytes(t *testing.T) {
	wantBytes := []byte{'o', 'k', 0xff, 0xfe, '!'}
	want := validRecord()
	want.Stdout = NewStream(int64(len(wantBytes)), true, wantBytes, 0, nil)

	got, err := Decode(bytes.NewReader(marshalRecord(t, want)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Stdout.HeadText != "ok\uFFFD!" {
		t.Fatalf("readable text = %q, want replacement text", got.Stdout.HeadText)
	}
	gotBytes, err := got.Stdout.HeadData()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("raw bytes = %v, want %v", gotBytes, wantBytes)
	}
}

func TestDecodeReadsLegacyV1WithWarningMetadata(t *testing.T) {
	artifact := `{
  "version": 1,
  "command": ["printf", "hello"],
  "directory": "/tmp/project",
  "started_at": "2026-08-17T12:00:00Z",
  "duration_ns": 10,
  "exit_code": 0,
  "stdout": "hello",
  "stderr": ""
}`

	got, err := Decode(strings.NewReader(artifact))
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != LegacyVersion || got.Stdout.HeadText != "hello" {
		t.Fatalf("legacy record = %#v", got)
	}
	if got.Stdout.ReceivedBytes != 5 || got.Stdout.OmittedBytes != 0 {
		t.Fatalf("legacy stream conversion = %#v", got.Stdout)
	}
}

func TestDecodeAcceptsPairedJSONSurrogate(t *testing.T) {
	artifact := `{
  "version": 1,
  "command": ["printf"],
  "directory": "/tmp/project",
  "started_at": "2026-08-17T12:00:00Z",
  "duration_ns": 0,
  "exit_code": 0,
  "stdout": "\uD83D\uDE00",
  "stderr": ""
}`
	got, err := Decode(strings.NewReader(artifact))
	if err != nil {
		t.Fatal(err)
	}
	if got.Stdout.HeadText != "😀" {
		t.Fatalf("stdout = %q, want emoji", got.Stdout.HeadText)
	}
}

func TestDecodeRejectsUnsupportedVersion(t *testing.T) {
	_, err := Decode(strings.NewReader(`{"version":3}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported record version") {
		t.Fatalf("error = %v, want unsupported version error", err)
	}
}

func TestDecodeRejectsMultipleValues(t *testing.T) {
	data := append(marshalRecord(t, validRecord()), []byte("\n{}\n")...)
	_, err := Decode(bytes.NewReader(data))
	if err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("error = %v, want multiple values error", err)
	}
}

func TestDecodeRejectsOversizedFileBeforeJSONDecode(t *testing.T) {
	data := bytes.Repeat([]byte{' '}, MaxRecordBytes+1)
	_, err := Decode(bytes.NewReader(data))
	if err == nil || !strings.Contains(err.Error(), "record exceeds") {
		t.Fatalf("error = %v, want size-limit error", err)
	}
}

func TestDecodeRejectsInvalidRawJSONUTF8(t *testing.T) {
	data := []byte{'{', '"', 0xff, '"', ':', '0', '}'}
	_, err := Decode(bytes.NewReader(data))
	if err == nil || !strings.Contains(err.Error(), "JSON is not valid UTF-8") {
		t.Fatalf("error = %v, want raw UTF-8 error", err)
	}
}

func TestDecodeRejectsExcessiveJSONNesting(t *testing.T) {
	artifact := strings.Repeat("[", maxJSONDepth+2) + "0" + strings.Repeat("]", maxJSONDepth+2)
	_, err := Decode(strings.NewReader(artifact))
	if err == nil || !strings.Contains(err.Error(), "nesting exceeds") {
		t.Fatalf("error = %v, want nesting-limit error", err)
	}
}

func TestDecodeRejectsDuplicateKeysAtAnyDepth(t *testing.T) {
	tests := []string{
		`{"version":2,"version":1}`,
		`{"version":2,"stdout":{"head_text":"a","head_text":"b"}}`,
		`{"version":2,"stdout":{"head_text":"a","\u0068ead_text":"b"}}`,
	}
	for _, artifact := range tests {
		_, err := Decode(strings.NewReader(artifact))
		if err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
			t.Errorf("Decode(%s) error = %v, want duplicate-key error", artifact, err)
		}
	}
}

func TestDecodeRejectsUnpairedSurrogateBeforeUnmarshal(t *testing.T) {
	for _, value := range []string{`\uD800`, `\uDC00`, `\uD800x`} {
		artifact := `{"version":2,"value":"` + value + `"}`
		_, err := Decode(strings.NewReader(artifact))
		if err == nil || !strings.Contains(err.Error(), "surrogate") {
			t.Errorf("Decode(%s) error = %v, want surrogate error", artifact, err)
		}
	}
}

func TestDecodeRejectsNonCanonicalByteCounts(t *testing.T) {
	data := string(marshalRecord(t, validRecord()))
	for _, token := range []string{"-0", "01", "1.0", "1e0", "9007199254740992"} {
		artifact := strings.Replace(data, `"received_bytes":5`, `"received_bytes":`+token, 1)
		if artifact == data {
			t.Fatal("test fixture did not contain expected received_bytes token")
		}
		if _, err := Decode(strings.NewReader(artifact)); err == nil {
			t.Errorf("byte count %q was accepted", token)
		}
	}
}

func TestDecodeRejectsRetainedChunkAboveReaderLimit(t *testing.T) {
	data := string(marshalRecord(t, validRecord()))
	data = strings.Replace(data, `"head_bytes":5`, `"head_bytes":500000000`, 1)
	data = strings.Replace(data, `"received_bytes":5`, `"received_bytes":500000000`, 1)

	_, err := Decode(strings.NewReader(data))
	if err == nil || !strings.Contains(err.Error(), "retained chunks exceed") {
		t.Fatalf("error = %v, want retained-chunk limit error", err)
	}
}

func TestDecodeRejectsUnknownV2Fields(t *testing.T) {
	data := string(marshalRecord(t, validRecord()))
	data = strings.Replace(data, `"capture_complete":true`, `"capture_complete":true,"encoding":"utf8"`, 1)

	_, err := Decode(strings.NewReader(data))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error = %v, want unknown-field error", err)
	}
}

func TestDecodeRejectsNullCommandArgument(t *testing.T) {
	data := string(marshalRecord(t, validRecord()))
	data = strings.Replace(data, `"command":["printf"`, `"command":[null`, 1)

	_, err := Decode(strings.NewReader(data))
	if err == nil || !strings.Contains(err.Error(), "command argument 0 is null") {
		t.Fatalf("error = %v, want null-command error", err)
	}
}

func TestDecodeRequiresGitDirtyWhenGitIsPresent(t *testing.T) {
	r := validRecord()
	r.Git = &GitContext{}
	data := string(marshalRecord(t, r))
	data = strings.Replace(data, `"git":{"dirty":false}`, `"git":{}`, 1)

	_, err := Decode(strings.NewReader(data))
	if err == nil || !strings.Contains(err.Error(), "git.dirty is missing") {
		t.Fatalf("error = %v, want required git.dirty error", err)
	}
}

func TestDecodeRequiresInterruptionSignal(t *testing.T) {
	data := string(marshalRecord(t, validRecord()))
	data = strings.Replace(data, `"exit_code":0`, `"exit_code":130,"interruption":{}`, 1)

	_, err := Decode(strings.NewReader(data))
	if err == nil || !strings.Contains(err.Error(), "interruption.signal is missing") {
		t.Fatalf("error = %v, want required interruption signal", err)
	}
}

func TestDecodeRejectsInconsistentInterruption(t *testing.T) {
	tests := map[string]Interruption{
		"exit code": {Signal: "SIGTERM"},
		"signal":    {Signal: "SIGKILL"},
	}
	for name, interruption := range tests {
		t.Run(name, func(t *testing.T) {
			r := validRecord()
			r.ExitCode = 130
			r.Interruption = &interruption
			if _, err := Decode(bytes.NewReader(marshalRecord(t, r))); err == nil {
				t.Fatal("inconsistent interruption was accepted")
			}
		})
	}
}

func TestDecodeRejectsExplicitEmptyRawField(t *testing.T) {
	data := string(marshalRecord(t, validRecord()))
	data = strings.Replace(data, `"head_text":"hello"`, `"head_text":"hello","head_raw_base64":""`, 1)

	_, err := Decode(strings.NewReader(data))
	if err == nil || !strings.Contains(err.Error(), "present but empty") {
		t.Fatalf("error = %v, want empty-raw-field error", err)
	}
}

func TestDecodeRejectsBase64NewlinesExplicitly(t *testing.T) {
	r := validRecord()
	r.Stdout = NewStream(1, true, []byte{0xff}, 0, nil)
	data := string(marshalRecord(t, r))
	data = strings.Replace(data, `/w==`, `/w==\n`, 1)

	_, err := Decode(strings.NewReader(data))
	if err == nil || !strings.Contains(err.Error(), "CR and LF") {
		t.Fatalf("error = %v, want explicit newline rejection", err)
	}
}

func TestStreamValidateRejectsInconsistentRepresentations(t *testing.T) {
	tests := map[string]Stream{
		"accounting": NewStream(6, true, []byte("hello"), 0, nil),
		"tail without omission": {
			ReceivedBytes: 1, CaptureComplete: true, HeadBytes: 0, HeadText: "",
			OmittedBytes: 0, TailBytes: 1, TailText: "x",
		},
		"invalid base64": {
			ReceivedBytes: 1, CaptureComplete: true, HeadBytes: 1, HeadText: "\uFFFD",
			HeadRawBase64: "!", OmittedBytes: 0, TailBytes: 0, TailText: "",
		},
		"raw length before decode": {
			ReceivedBytes: 1, CaptureComplete: true, HeadBytes: 1, HeadText: "\uFFFD",
			HeadRawBase64: strings.Repeat("A", 1<<20), OmittedBytes: 0, TailBytes: 0, TailText: "",
		},
		"noncanonical padding bits": {
			ReceivedBytes: 1, CaptureComplete: true, HeadBytes: 1, HeadText: "\uFFFD",
			HeadRawBase64: "/x==", OmittedBytes: 0, TailBytes: 0, TailText: "",
		},
		"redundant raw": {
			ReceivedBytes: 1, CaptureComplete: true, HeadBytes: 1, HeadText: "a",
			HeadRawBase64: "YQ==", OmittedBytes: 0, TailBytes: 0, TailText: "",
		},
		"mismatched text": {
			ReceivedBytes: 1, CaptureComplete: true, HeadBytes: 1, HeadText: "wrong",
			HeadRawBase64: "/w==", OmittedBytes: 0, TailBytes: 0, TailText: "",
		},
	}
	for name, stream := range tests {
		t.Run(name, func(t *testing.T) {
			if err := stream.Validate("stdout"); err == nil {
				t.Fatal("invalid stream was accepted")
			}
		})
	}
}

func TestWriteFileReplacesExistingRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latest.json")
	first := validRecord()
	if err := WriteFile(path, first); err != nil {
		t.Fatal(err)
	}
	second := validRecord()
	second.ExitCode = 9
	if err := WriteFile(path, second); err != nil {
		t.Fatal(err)
	}

	got, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExitCode != 9 {
		t.Fatalf("exit code = %d, want 9", got.ExitCode)
	}
}

func TestWriteFileWithContentIDMatchesStrictReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")
	writtenID, err := WriteFileWithContentID(path, validRecord())
	if err != nil {
		t.Fatal(err)
	}
	_, readID, err := ReadFileWithContentID(path)
	if err != nil {
		t.Fatal(err)
	}
	if writtenID != readID {
		t.Fatalf("written content ID = %q, read content ID = %q", writtenID, readID)
	}
}

func TestWriteFileRequiresProducerVersion(t *testing.T) {
	r := validRecord()
	r.RunprintVersion = ""
	err := WriteFile(filepath.Join(t.TempDir(), "latest.json"), r)
	if err == nil || !strings.Contains(err.Error(), "runprint_version") {
		t.Fatalf("error = %v, want missing producer version", err)
	}
}

func TestWriteFileRoundTripsAdversarialMaximumStreams(t *testing.T) {
	head := bytes.Repeat([]byte{'<'}, 64<<10)
	tail := bytes.Repeat([]byte{'<'}, 192<<10)
	head[len(head)-1] = 0xff
	tail[len(tail)-1] = 0xff
	stream := NewStream(int64(len(head)+1+len(tail)), true, head, 1, tail)

	r := validRecord()
	r.Stdout = stream
	r.Stderr = stream
	r.RunprintVersion = "v" + strings.Repeat("x", maxProducerVersion-1)
	r.Directory = strings.Repeat("<", maxMetadataStringBytes)
	r.Git = &GitContext{
		Commit: strings.Repeat("<", maxMetadataStringBytes),
		Branch: strings.Repeat("<", maxMetadataStringBytes),
		Dirty:  true,
	}
	for range 300 {
		r.Command = append(r.Command, strings.Repeat("<", 1000))
	}

	path := filepath.Join(t.TempDir(), "maximum.json")
	if err := WriteFile(path, r); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= 6<<20 {
		t.Fatalf("fixture size = %d, want adversarial envelope above 6 MiB", info.Size())
	}
	got, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, r) {
		t.Fatal("strict reader did not recover the adversarial writer record")
	}
}

func TestValidateRejectsOversizedOrLossyCommandMetadata(t *testing.T) {
	tests := map[string]string{
		"encoded size":  strings.Repeat("<", MaxCommandJSONBytes/5),
		"invalid UTF-8": string([]byte{0xff}),
	}
	for name, argument := range tests {
		t.Run(name, func(t *testing.T) {
			r := validRecord()
			r.Command = []string{"command", argument}
			if err := r.Validate(); err == nil {
				t.Fatal("unsupported command metadata was accepted")
			}
		})
	}
}

func FuzzDecodeDoesNotPanic(f *testing.F) {
	f.Add(marshalRecord(f, validRecord()))
	f.Add([]byte(`{"version":2,"stdout":{"received_bytes":500000000}}`))
	f.Add([]byte(`{"version":2,"version":1}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Decode(bytes.NewReader(data))
	})
}

type fataler interface {
	Helper()
	Fatal(...any)
}

func marshalRecord(t fataler, r Record) []byte {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func validRecord() Record {
	return Record{
		Version:         CurrentVersion,
		RunprintVersion: DevelopmentVersion,
		Command:         []string{"printf", "hello world"},
		Directory:       "/tmp/project",
		StartedAt:       time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC),
		Duration:        10 * time.Millisecond,
		ExitCode:        0,
		Stdout:          NewStream(5, true, []byte("hello"), 0, nil),
		Stderr:          NewStream(0, true, nil, 0, nil),
	}
}
