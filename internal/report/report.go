package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/MorisKyiv/runprint/internal/record"
)

// Write renders a terminal-safe, human-readable execution report.
func Write(output io.Writer, r record.Record) error {
	return writeTerminal(output, r, false)
}

// WriteRaw renders a report without escaping or prefixing captured stdout and
// stderr. Metadata remains escaped. Raw mode is only for trusted records.
func WriteRaw(output io.Writer, r record.Record) error {
	return writeTerminal(output, r, true)
}

func writeTerminal(output io.Writer, r record.Record, rawStreams bool) error {
	if err := r.Validate(); err != nil {
		return err
	}
	command, err := safeCommandJSON(r.Command)
	if err != nil {
		return err
	}

	var header strings.Builder
	fmt.Fprintf(&header, "command: %s\n", command)
	fmt.Fprintf(&header, "directory: %s\n", EscapeInline(r.Directory))
	fmt.Fprintf(&header, "started: %s\n", r.StartedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
	fmt.Fprintf(&header, "duration: %s\n", r.Duration)
	fmt.Fprintf(&header, "exit code: %d\n", r.ExitCode)
	fmt.Fprintf(&header, "record format: %d\n", r.Version)
	fmt.Fprintf(&header, "runprint version: %s\n", EscapeInline(valueOrUnknown(r.RunprintVersion)))
	if r.Interruption != nil {
		fmt.Fprintf(&header, "interrupted by Runprint: %s\n", EscapeInline(r.Interruption.Signal))
	}
	if r.Git != nil {
		fmt.Fprintf(&header, "git commit: %s\n", EscapeInline(valueOrUnknown(r.Git.Commit)))
		fmt.Fprintf(&header, "git branch: %s\n", EscapeInline(valueOrUnknown(r.Git.Branch)))
		fmt.Fprintf(&header, "git dirty: %t\n", r.Git.Dirty)
	}
	if r.Version == record.LegacyVersion {
		header.WriteString("warning: legacy schema v1 has no byte accounting; invalid UTF-8 may already have been replaced\n")
	}

	if _, err := io.WriteString(output, header.String()); err != nil {
		return err
	}
	if err := writeTerminalStream(output, "stdout", r.Stdout, rawStreams); err != nil {
		return err
	}
	return writeTerminalStream(output, "stderr", r.Stderr, rawStreams)
}

func writeTerminalStream(output io.Writer, name string, stream record.Stream, raw bool) error {
	if _, err := fmt.Fprintf(output, "\n%s:\n", name); err != nil {
		return err
	}
	if !stream.CaptureComplete {
		if _, err := io.WriteString(output, "! capture incomplete; byte count covers only data received by Runprint\n"); err != nil {
			return err
		}
	}
	if stream.HasInvalidUTF8() && !raw {
		if _, err := io.WriteString(output, "! invalid UTF-8 replaced for display; exact retained bytes remain in the record\n"); err != nil {
			return err
		}
	}

	if stream.ReceivedBytes == 0 {
		_, err := io.WriteString(output, "(empty)\n")
		return err
	}
	if raw {
		return writeRawStream(output, stream)
	}
	if err := writePrefixed(output, EscapeControls(stream.HeadText)); err != nil {
		return err
	}
	if stream.Truncated() {
		if _, err := fmt.Fprintf(output, "... %d captured bytes omitted ...\n", stream.OmittedBytes); err != nil {
			return err
		}
	}
	return writePrefixed(output, EscapeControls(stream.TailText))
}

func writeRawStream(output io.Writer, stream record.Stream) error {
	head, err := stream.HeadData()
	if err != nil {
		return err
	}
	tail, err := stream.TailData()
	if err != nil {
		return err
	}
	if err := writeRawPart(output, head); err != nil {
		return err
	}
	if stream.Truncated() {
		if _, err := fmt.Fprintf(output, "... %d captured bytes omitted ...\n", stream.OmittedBytes); err != nil {
			return err
		}
	}
	return writeRawPart(output, tail)
}

func writeRawPart(output io.Writer, value []byte) error {
	if len(value) == 0 {
		return nil
	}
	if _, err := output.Write(value); err != nil {
		return err
	}
	if value[len(value)-1] != '\n' {
		_, err := io.WriteString(output, "\n")
		return err
	}
	return nil
}

// writePrefixed gives every untrusted display line a structural prefix. A
// captured line can therefore resemble, but cannot forge, Runprint metadata or
// an omission marker.
func writePrefixed(output io.Writer, value string) error {
	for len(value) > 0 {
		lineEnd := strings.IndexByte(value, '\n')
		if lineEnd < 0 {
			_, err := fmt.Fprintf(output, "| %s\n", value)
			return err
		}
		if _, err := fmt.Fprintf(output, "| %s\n", value[:lineEnd]); err != nil {
			return err
		}
		value = value[lineEnd+1:]
	}
	return nil
}

// WriteMarkdown renders a portable report suitable for pasting into an issue.
// It escapes terminal controls and abbreviates common home-directory prefixes,
// but it does not scan for secrets.
func WriteMarkdown(output io.Writer, r record.Record) error {
	return writeMarkdown(output, r, "")
}

// WriteMarkdownWithContentID renders a portable report and labels it with the
// SHA-256 identifier of the exact JSON artifact from which it was produced.
func WriteMarkdownWithContentID(output io.Writer, r record.Record, contentID string) error {
	return writeMarkdown(output, r, contentID)
}

func writeMarkdown(output io.Writer, r record.Record, contentID string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	command, err := safeCommandJSON(r.Command)
	if err != nil {
		return err
	}

	if _, err := io.WriteString(output, "## Runprint execution report\n\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "- **Exit code:** `%d`\n", r.ExitCode); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "- **Started:** `%s`\n", r.StartedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "- **Duration:** `%s`\n", r.Duration); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "- **Record format:** `%d`\n", r.Version); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "- **Runprint version:** `%s`\n", EscapeInline(valueOrUnknown(r.RunprintVersion))); err != nil {
		return err
	}
	if contentID != "" {
		if _, err := fmt.Fprintf(output, "- **Content ID:** `%s`\n", EscapeInline(contentID)); err != nil {
			return err
		}
	}
	if r.Interruption != nil {
		if _, err := fmt.Fprintf(output, "- **Interrupted by Runprint:** `%s`\n", EscapeInline(r.Interruption.Signal)); err != nil {
			return err
		}
	}
	if r.Git != nil {
		if _, err := fmt.Fprintf(output, "- **Git dirty:** `%t`\n", r.Git.Dirty); err != nil {
			return err
		}
	}
	if r.Version == record.LegacyVersion {
		if _, err := io.WriteString(output, "\n> **Legacy record:** schema v1 had no byte accounting; invalid UTF-8 may already have been replaced while recording.\n"); err != nil {
			return err
		}
	}

	if err := writeMarkdownBlock(output, "Command", "json", command); err != nil {
		return err
	}
	if err := writeMarkdownBlock(output, "Working directory", "text", EscapeInline(abbreviateHome(r.Directory))); err != nil {
		return err
	}
	if r.Git != nil {
		git := fmt.Sprintf("commit: %s\nbranch: %s", EscapeInline(valueOrUnknown(r.Git.Commit)), EscapeInline(valueOrUnknown(r.Git.Branch)))
		if err := writeMarkdownBlock(output, "Git", "text", git); err != nil {
			return err
		}
	}
	if err := writeMarkdownStream(output, "stdout", r.Stdout); err != nil {
		return err
	}
	if err := writeMarkdownStream(output, "stderr", r.Stderr); err != nil {
		return err
	}

	_, err = io.WriteString(output, "\n> Review command arguments, output, and paths for sensitive data before sharing. Runprint does not scan for secrets.\n>\n> This report is not tamper-evident. Its structure is validated, but its origin and contents are not authenticated.\n")
	return err
}

func writeMarkdownStream(output io.Writer, name string, stream record.Stream) error {
	if _, err := fmt.Fprintf(output, "\n### %s\n", name); err != nil {
		return err
	}
	if !stream.CaptureComplete {
		if _, err := io.WriteString(output, "\n> **Capture incomplete:** byte count covers only data received by Runprint.\n"); err != nil {
			return err
		}
	}
	if stream.HasInvalidUTF8() {
		if _, err := io.WriteString(output, "\n> **Invalid UTF-8:** replacement characters are shown; exact retained bytes remain in the JSON record.\n"); err != nil {
			return err
		}
	}

	if stream.ReceivedBytes == 0 {
		return writeMarkdownFence(output, "text", "(empty)")
	}
	if stream.HeadBytes > 0 {
		if err := writeMarkdownFence(output, "text", EscapeControls(stream.HeadText)); err != nil {
			return err
		}
	}
	if stream.Truncated() {
		if _, err := fmt.Fprintf(output, "\n> **%d captured bytes omitted.**\n", stream.OmittedBytes); err != nil {
			return err
		}
	}
	if stream.TailBytes > 0 {
		return writeMarkdownFence(output, "text", EscapeControls(stream.TailText))
	}
	return nil
}

func valueOrUnknown(value string) string {
	if value == "" {
		return "(unknown)"
	}
	return value
}

// EscapeControls makes terminal control characters visible while preserving
// ordinary Unicode text, newlines, and tabs.
func EscapeControls(value string) string {
	return escape(value, true)
}

// EscapeInline makes all non-printing characters visible so untrusted
// metadata cannot create additional report lines.
func EscapeInline(value string) string {
	return escape(value, false)
}

func escape(value string, preserveWhitespace bool) string {
	var escaped strings.Builder
	for _, r := range value {
		switch {
		case preserveWhitespace && (r == '\n' || r == '\t'):
			escaped.WriteRune(r)
		case !unicode.IsPrint(r) || isBidiControl(r):
			writeRuneEscape(&escaped, r)
		default:
			escaped.WriteRune(r)
		}
	}
	return escaped.String()
}

func safeCommandJSON(command []string) (string, error) {
	data, err := json.Marshal(command)
	if err != nil {
		return "", err
	}
	return EscapeControls(string(data)), nil
}

func writeRuneEscape(output *strings.Builder, r rune) {
	switch {
	case r <= 0xff:
		fmt.Fprintf(output, "\\x%02x", r)
	case r <= 0xffff:
		fmt.Fprintf(output, "\\u%04x", r)
	default:
		fmt.Fprintf(output, "\\U%08x", r)
	}
}

func isBidiControl(r rune) bool {
	return r == '\u061c' || r == '\u200e' || r == '\u200f' ||
		(r >= '\u202a' && r <= '\u202e') ||
		(r >= '\u2066' && r <= '\u2069')
}

func writeMarkdownBlock(output io.Writer, title, language, content string) error {
	if _, err := fmt.Fprintf(output, "\n### %s\n", title); err != nil {
		return err
	}
	return writeMarkdownFence(output, language, content)
}

func writeMarkdownFence(output io.Writer, language, content string) error {
	if content == "" {
		content = "(empty)"
	}
	fence := strings.Repeat("`", longestBacktickRun(content)+1)
	if len(fence) < 3 {
		fence = "```"
	}

	if _, err := fmt.Fprintf(output, "\n%s%s\n", fence, language); err != nil {
		return err
	}
	if _, err := io.WriteString(output, content); err != nil {
		return err
	}
	if !strings.HasSuffix(content, "\n") {
		if _, err := io.WriteString(output, "\n"); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(output, "%s\n", fence)
	return err
}

func longestBacktickRun(value string) int {
	longest := 0
	current := 0
	for _, r := range value {
		if r == '`' {
			current++
			if current > longest {
				longest = current
			}
			continue
		}
		current = 0
	}
	return longest
}

func abbreviateHome(path string) string {
	normalized := strings.ReplaceAll(path, "\\", "/")
	for _, prefix := range []string{"/home/", "/Users/"} {
		if strings.HasPrefix(normalized, prefix) {
			return abbreviatePathAfterUser(normalized, len(prefix))
		}
	}
	if normalized == "/root" {
		return "~"
	}
	if strings.HasPrefix(normalized, "/root/") {
		return "~" + strings.TrimPrefix(normalized, "/root")
	}
	if len(normalized) >= len("C:/Users/") && normalized[1] == ':' && strings.EqualFold(normalized[2:9], "/Users/") {
		return abbreviatePathAfterUser(normalized, len("C:/Users/"))
	}
	return path
}

func abbreviatePathAfterUser(path string, userStart int) string {
	remainder := path[userStart:]
	separator := strings.IndexByte(remainder, '/')
	if separator < 0 {
		return "~"
	}
	return "~" + remainder[separator:]
}
