package report

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MorisKyiv/runprint/internal/record"
)

func TestWriteRendersMetadataAndStructurallyPrefixedStreams(t *testing.T) {
	r := reportRecord()
	r.Stdout = textStream("partial output")

	var output bytes.Buffer
	if err := Write(&output, r); err != nil {
		t.Fatal(err)
	}

	wantFragments := []string{
		`command: ["go","test","./..."]`,
		"started: 2026-08-17T12:00:00.000000123Z",
		"duration: 2.5s",
		"record format: 2",
		"git commit: deadbeef",
		"stdout:\n| partial output\n",
		"stderr:\n(empty)\n",
	}
	for _, fragment := range wantFragments {
		if !strings.Contains(output.String(), fragment) {
			t.Errorf("output does not contain %q:\n%s", fragment, output.String())
		}
	}
}

func TestWriteEscapesUntrustedTerminalControls(t *testing.T) {
	r := reportRecord()
	r.Command = []string{"printf", "\x1b[2J"}
	r.Directory = "/tmp/\x1b[31mproject\nexit code: 0"
	r.Stdout = textStream("before\x1b[2Jafter\rhidden\x07")
	r.Stderr = textStream("direction\u202ereversed\u200bhidden")
	r.Git.Branch = "main\x1b[0m\tforged"

	var output bytes.Buffer
	if err := Write(&output, r); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, control := range []string{"\x1b", "\r", "\x07", "\u202e", "\u200b"} {
		if strings.Contains(got, control) {
			t.Fatalf("output contains raw control %q: %q", control, got)
		}
	}
	for _, escaped := range []string{`\x1b[2J`, `\x0a`, `\x09`, `\x0d`, `\x07`, `\u202e`, `\u200b`} {
		if !strings.Contains(got, escaped) {
			t.Errorf("output does not contain visible escape %q: %q", escaped, got)
		}
	}
	if !strings.Contains(got, `command: ["printf","\u001b[2J"]`) {
		t.Fatalf("command does not preserve JSON distinction for ESC: %q", got)
	}
	if strings.Contains(got, "directory: /tmp/\\x1b[31mproject\nexit code: 0") {
		t.Fatalf("metadata injected an additional report line: %q", got)
	}
}

func TestWriteMakesForgedOmissionMarkerVisiblyUntrusted(t *testing.T) {
	fake := "... 5 captured bytes omitted ..."
	r := reportRecord()
	r.Stdout = record.NewStream(
		int64(len(fake)+len("after")+5),
		true,
		[]byte(fake),
		5,
		[]byte("after"),
	)

	var output bytes.Buffer
	if err := Write(&output, r); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.Contains(got, "| "+fake+"\n") {
		t.Fatalf("captured marker lacks untrusted prefix:\n%s", got)
	}
	if !strings.Contains(got, "\n"+fake+"\n") {
		t.Fatalf("trusted omission marker is missing:\n%s", got)
	}
}

func TestWriteRawPreservesExactRetainedBytes(t *testing.T) {
	r := reportRecord()
	want := []byte{0xff, 0x00, '\n'}
	r.Stdout = record.NewStream(int64(len(want)), true, want, 0, nil)
	r.Directory = "/tmp/project\nforged"

	var output bytes.Buffer
	if err := WriteRaw(&output, r); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), want) {
		t.Fatalf("raw output did not preserve bytes: %v", output.Bytes())
	}
	if strings.Contains(output.String(), "directory: /tmp/project\nforged") {
		t.Fatalf("raw output did not escape metadata: %q", output.String())
	}
	if !strings.Contains(output.String(), `directory: /tmp/project\x0aforged`) {
		t.Fatalf("raw output does not contain visible metadata escape: %q", output.String())
	}
}

func TestWriteMarkdownIsPortableAndFenceSafe(t *testing.T) {
	r := reportRecord()
	r.Directory = "/home/alice/client/project"
	r.StartedAt = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	r.Duration = 3 * time.Second
	r.Stdout = textStream("before\n```\nafter\x1b[2J")
	r.Stderr = textStream("failed")
	r.Git.Dirty = true

	var output bytes.Buffer
	if err := WriteMarkdown(&output, r); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	want, err := os.ReadFile("testdata/portable.md.golden")
	if err != nil {
		t.Fatal(err)
	}
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if got != string(want) {
		t.Errorf("Markdown output differs from golden file:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
	if strings.Contains(got, "\x1b") {
		t.Fatalf("Markdown contains a raw escape character: %q", got)
	}
}

func TestWriteMarkdownIncludesProvidedContentID(t *testing.T) {
	var output bytes.Buffer
	if err := WriteMarkdownWithContentID(&output, reportRecord(), "sha256:abc123"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "- **Content ID:** `sha256:abc123`") {
		t.Fatalf("Markdown content ID is missing:\n%s", output.String())
	}
}

func TestWriteMarkdownKeepsOmissionMarkerOutsideUntrustedFence(t *testing.T) {
	fake := "> **7 captured bytes omitted.**"
	r := reportRecord()
	head := fake + "\n```"
	r.Stdout = record.NewStream(
		int64(len(head)+len("tail")+7),
		true,
		[]byte(head),
		7,
		[]byte("tail"),
	)

	var output bytes.Buffer
	if err := WriteMarkdown(&output, r); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.Contains(got, "\n> **7 captured bytes omitted.**\n") {
		t.Fatalf("structural omission marker is missing:\n%s", got)
	}
	if !strings.Contains(got, fake+"\n```") {
		t.Fatalf("forged marker was not retained inside the code content:\n%s", got)
	}
}

func TestWriteWarnsAboutLegacyAndIncompleteCapture(t *testing.T) {
	legacy := reportRecord()
	legacy.Version = record.LegacyVersion
	var legacyOutput bytes.Buffer
	if err := WriteMarkdown(&legacyOutput, legacy); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(legacyOutput.String(), "Legacy record") || !strings.Contains(legacyOutput.String(), "invalid UTF-8 may already") {
		t.Fatalf("legacy warning missing:\n%s", legacyOutput.String())
	}

	incomplete := reportRecord()
	incomplete.Stdout.CaptureComplete = false
	var incompleteOutput bytes.Buffer
	if err := WriteMarkdown(&incompleteOutput, incomplete); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(incompleteOutput.String(), "Capture incomplete") {
		t.Fatalf("incomplete-capture warning missing:\n%s", incompleteOutput.String())
	}
}

func TestWriteWarnsWhenReadableTextReplacesInvalidUTF8(t *testing.T) {
	r := reportRecord()
	r.Stdout = record.NewStream(1, true, []byte{0xff}, 0, nil)

	var output bytes.Buffer
	if err := WriteMarkdown(&output, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Invalid UTF-8") || !strings.Contains(output.String(), "�") {
		t.Fatalf("invalid-UTF-8 disclosure missing:\n%s", output.String())
	}
}

func TestRenderersIdentifyProducerAndRunprintInterruption(t *testing.T) {
	r := reportRecord()
	r.RunprintVersion = "v0.1.0"
	r.ExitCode = 143
	r.Interruption = &record.Interruption{Signal: "SIGTERM"}

	var terminal bytes.Buffer
	if err := Write(&terminal, r); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"runprint version: v0.1.0", "interrupted by Runprint: SIGTERM"} {
		if !strings.Contains(terminal.String(), fragment) {
			t.Errorf("terminal report missing %q:\n%s", fragment, terminal.String())
		}
	}

	var markdown bytes.Buffer
	if err := WriteMarkdown(&markdown, r); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"**Runprint version:** `v0.1.0`",
		"**Interrupted by Runprint:** `SIGTERM`",
		"This report is not tamper-evident",
	} {
		if !strings.Contains(markdown.String(), fragment) {
			t.Errorf("Markdown report missing %q:\n%s", fragment, markdown.String())
		}
	}
}

func TestAbbreviateHome(t *testing.T) {
	tests := map[string]string{
		"/home/alice/project":    "~/project",
		"/Users/alice/project":   "~/project",
		"/root/project":          "~/project",
		`C:\Users\Alice\project`: "~/project",
		"/srv/project":           "/srv/project",
	}
	for input, want := range tests {
		if got := abbreviateHome(input); got != want {
			t.Errorf("abbreviateHome(%q) = %q, want %q", input, got, want)
		}
	}
}

func reportRecord() record.Record {
	return record.Record{
		Version:         record.CurrentVersion,
		RunprintVersion: record.DevelopmentVersion,
		Command:         []string{"go", "test", "./..."},
		Directory:       "/src/runprint",
		StartedAt:       time.Date(2026, 8, 17, 12, 0, 0, 123, time.UTC),
		Duration:        2500 * time.Millisecond,
		ExitCode:        1,
		Stdout:          textStream(""),
		Stderr:          textStream(""),
		Git: &record.GitContext{
			Commit: "deadbeef",
			Branch: "main",
		},
	}
}

func textStream(value string) record.Stream {
	return record.NewStream(int64(len(value)), true, []byte(value), 0, nil)
}
