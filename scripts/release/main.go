package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	productName            = "runprint"
	checksumFileName       = "checksums.txt"
	maxReleaseVersionBytes = 64
)

var (
	releaseVersionPattern = regexp.MustCompile(
		`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`,
	)
	archiveTime    = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
	releaseTargets = []target{
		{goos: "darwin", goarch: "amd64"},
		{goos: "darwin", goarch: "arm64"},
		{goos: "linux", goarch: "amd64"},
		{goos: "linux", goarch: "arm64"},
		{goos: "windows", goarch: "amd64"},
		{goos: "windows", goarch: "arm64"},
	}
)

type target struct {
	goos   string
	goarch string
}

type options struct {
	root    string
	output  string
	version string
}

type archiveEntry struct {
	name string
	mode os.FileMode
	data []byte
}

func main() {
	var opts options
	flag.StringVar(&opts.root, "root", ".", "project root")
	flag.StringVar(&opts.output, "output", "dist", "empty output directory")
	flag.StringVar(&opts.version, "version", "", "release version such as v0.1.0")
	flag.Parse()

	if err := packageRelease(context.Background(), opts, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func packageRelease(ctx context.Context, opts options, log io.Writer) error {
	if err := validateVersion(opts.version); err != nil {
		return err
	}

	root, err := filepath.Abs(opts.root)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}
	output := opts.output
	if !filepath.IsAbs(output) {
		output = filepath.Join(root, output)
	}
	if err := prepareEmptyDirectory(output); err != nil {
		return err
	}

	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		return fmt.Errorf("read README.md: %w", err)
	}
	license, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		return fmt.Errorf("read LICENSE: %w", err)
	}

	stage := filepath.Join(output, ".staging")
	if err := os.Mkdir(stage, 0o755); err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(stage)

	versionLabel := strings.TrimPrefix(opts.version, "v")
	artifacts := make([]string, 0, len(releaseTargets))
	for _, buildTarget := range releaseTargets {
		base := fmt.Sprintf(
			"%s_%s_%s_%s",
			productName,
			versionLabel,
			buildTarget.goos,
			buildTarget.goarch,
		)
		binaryFileName := productName
		if buildTarget.goos == "windows" {
			binaryFileName += ".exe"
		}
		binaryPath := filepath.Join(stage, base, binaryFileName)
		if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
			return fmt.Errorf("create target staging directory: %w", err)
		}
		if err := buildBinary(ctx, root, binaryPath, opts.version, buildTarget); err != nil {
			return err
		}
		if buildTarget.goos == runtime.GOOS && buildTarget.goarch == runtime.GOARCH {
			if err := verifyNativeVersion(binaryPath, opts.version); err != nil {
				return err
			}
		}

		binary, err := os.ReadFile(binaryPath)
		if err != nil {
			return fmt.Errorf("read built %s/%s binary: %w", buildTarget.goos, buildTarget.goarch, err)
		}
		entries := []archiveEntry{
			{name: path.Join(base, "LICENSE"), mode: 0o644, data: license},
			{name: path.Join(base, "README.md"), mode: 0o644, data: readme},
			{name: path.Join(base, binaryFileName), mode: 0o755, data: binary},
		}

		extension := ".tar.gz"
		if buildTarget.goos == "windows" {
			extension = ".zip"
		}
		artifactName := base + extension
		artifactPath := filepath.Join(output, artifactName)
		if err := writeArchive(artifactPath, entries); err != nil {
			return fmt.Errorf("write %s: %w", artifactName, err)
		}
		if err := verifyArchive(artifactPath, entries); err != nil {
			return fmt.Errorf("verify %s: %w", artifactName, err)
		}
		artifacts = append(artifacts, artifactName)
		fmt.Fprintf(log, "packaged %s\n", artifactName)
	}

	if err := os.RemoveAll(stage); err != nil {
		return fmt.Errorf("remove staging directory: %w", err)
	}
	if err := writeChecksums(output, artifacts); err != nil {
		return err
	}
	if err := verifyOutputSet(output, artifacts); err != nil {
		return err
	}
	fmt.Fprintf(log, "wrote %s for %d artifacts\n", checksumFileName, len(artifacts))
	return nil
}

func validateVersion(version string) error {
	if len(version) > maxReleaseVersionBytes {
		return fmt.Errorf("version %q exceeds the %d-byte release limit", version, maxReleaseVersionBytes)
	}
	if !releaseVersionPattern.MatchString(version) {
		return fmt.Errorf("version %q is not a canonical v-prefixed release version without build metadata", version)
	}
	_, prerelease, hasPrerelease := strings.Cut(version, "-")
	if hasPrerelease {
		for _, identifier := range strings.Split(prerelease, ".") {
			if len(identifier) > 1 && identifier[0] == '0' && decimal(identifier) {
				return fmt.Errorf("version %q has a numeric pre-release identifier with a leading zero", version)
			}
		}
	}
	return nil
}

func decimal(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return value != ""
}

func prepareEmptyDirectory(directory string) error {
	info, err := os.Stat(directory)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("inspect output directory: %w", err)
		}
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}
		return nil
	}
	if !info.IsDir() {
		return fmt.Errorf("output path %s is not a directory", directory)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read output directory: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("output directory %s is not empty", directory)
	}
	return nil
}

func buildBinary(
	ctx context.Context,
	root string,
	output string,
	version string,
	buildTarget target,
) error {
	command := exec.CommandContext(
		ctx,
		"go",
		"build",
		"-trimpath",
		"-buildvcs=false",
		"-ldflags", "-s -w -X main.version="+version,
		"-o", output,
		"./cmd/runprint",
	)
	command.Dir = root
	command.Env = releaseEnvironment(os.Environ(), map[string]string{
		"CGO_ENABLED":  "0",
		"GODEBUG":      "",
		"GOENV":        "off",
		"GOFIPS140":    "off",
		"GOARCH":       buildTarget.goarch,
		"GOAMD64":      "v1",
		"GOARM64":      "v8.0",
		"GOEXPERIMENT": "",
		"GOFLAGS":      "",
		"GOOS":         buildTarget.goos,
		"GOTELEMETRY":  "off",
		"GOTOOLCHAIN":  "local",
		"GOWORK":       "off",
	})
	combined, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"build %s/%s: %w\n%s",
			buildTarget.goos,
			buildTarget.goarch,
			err,
			combined,
		)
	}
	return nil
}

func releaseEnvironment(environment []string, replacements map[string]string) []string {
	allowed := map[string]bool{
		"APPDATA":      true,
		"COMSPEC":      true,
		"GOCACHE":      true,
		"GOMODCACHE":   true,
		"GOPATH":       true,
		"GOROOT":       true,
		"HOME":         true,
		"LOCALAPPDATA": true,
		"PATH":         true,
		"PATHEXT":      true,
		"SYSTEMROOT":   true,
		"TEMP":         true,
		"TMP":          true,
		"TMPDIR":       true,
		"USERPROFILE":  true,
		"WINDIR":       true,
	}
	result := make([]string, 0, len(environment)+len(replacements))
	for _, item := range environment {
		key, _, _ := strings.Cut(item, "=")
		canonicalKey := strings.ToUpper(key)
		if !allowed[canonicalKey] {
			continue
		}
		replaced := false
		for replacement := range replacements {
			if strings.EqualFold(key, replacement) {
				replaced = true
				break
			}
		}
		if !replaced {
			result = append(result, item)
		}
	}
	keys := make([]string, 0, len(replacements))
	for key := range replacements {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result = append(result, key+"="+replacements[key])
	}
	return result
}

func verifyNativeVersion(binaryPath, version string) error {
	command := exec.Command(binaryPath, "version")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run native release binary: %w\n%s", err, output)
	}
	want := "runprint " + version + "\n"
	if string(output) != want {
		return fmt.Errorf("native release version = %q, want %q", output, want)
	}
	return nil
}

func writeArchive(filePath string, entries []archiveEntry) error {
	if strings.HasSuffix(filePath, ".zip") {
		return writeZip(filePath, entries)
	}
	if strings.HasSuffix(filePath, ".tar.gz") {
		return writeTarGz(filePath, entries)
	}
	return fmt.Errorf("unsupported archive extension for %s", filePath)
}

func sortedEntries(entries []archiveEntry) []archiveEntry {
	ordered := append([]archiveEntry(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].name < ordered[j].name
	})
	return ordered
}

func writeTarGz(filePath string, entries []archiveEntry) (err error) {
	output, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = output.Close()
		}
		if err != nil {
			_ = os.Remove(filePath)
		}
	}()

	compressed, err := gzip.NewWriterLevel(output, gzip.BestCompression)
	if err != nil {
		return err
	}
	compressed.Header.ModTime = time.Unix(0, 0).UTC()
	compressed.Header.OS = 255
	archive := tar.NewWriter(compressed)
	for _, entry := range sortedEntries(entries) {
		header := &tar.Header{
			Name:       entry.name,
			Mode:       int64(entry.mode.Perm()),
			Size:       int64(len(entry.data)),
			ModTime:    archiveTime,
			Typeflag:   tar.TypeReg,
			Format:     tar.FormatUSTAR,
			AccessTime: time.Time{},
			ChangeTime: time.Time{},
		}
		if err = archive.WriteHeader(header); err != nil {
			return err
		}
		if _, err = archive.Write(entry.data); err != nil {
			return err
		}
	}
	if err = archive.Close(); err != nil {
		return err
	}
	if err = compressed.Close(); err != nil {
		return err
	}
	err = output.Close()
	closed = true
	return err
}

func writeZip(filePath string, entries []archiveEntry) (err error) {
	output, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = output.Close()
		}
		if err != nil {
			_ = os.Remove(filePath)
		}
	}()

	archive := zip.NewWriter(output)
	for _, entry := range sortedEntries(entries) {
		header := &zip.FileHeader{
			Name:   entry.name,
			Method: zip.Deflate,
		}
		header.SetMode(entry.mode)
		header.SetModTime(archiveTime)
		writer, createErr := archive.CreateHeader(header)
		if createErr != nil {
			return createErr
		}
		if _, err = writer.Write(entry.data); err != nil {
			return err
		}
	}
	if err = archive.Close(); err != nil {
		return err
	}
	err = output.Close()
	closed = true
	return err
}

func verifyArchive(filePath string, entries []archiveEntry) error {
	if strings.HasSuffix(filePath, ".zip") {
		return verifyZip(filePath, entries)
	}
	if strings.HasSuffix(filePath, ".tar.gz") {
		return verifyTarGz(filePath, entries)
	}
	return fmt.Errorf("unsupported archive extension for %s", filePath)
}

func expectedEntries(entries []archiveEntry) map[string]archiveEntry {
	expected := make(map[string]archiveEntry, len(entries))
	for _, entry := range entries {
		expected[entry.name] = entry
	}
	return expected
}

func verifyTarGz(filePath string, entries []archiveEntry) error {
	input, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer input.Close()
	compressed, err := gzip.NewReader(input)
	if err != nil {
		return err
	}
	defer compressed.Close()

	expected := expectedEntries(entries)
	archive := tar.NewReader(compressed)
	for {
		header, nextErr := archive.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nextErr
		}
		entry, ok := expected[header.Name]
		if !ok {
			return fmt.Errorf("unexpected archive entry %q", header.Name)
		}
		if os.FileMode(header.Mode).Perm() != entry.mode.Perm() {
			return fmt.Errorf("entry %q mode = %o, want %o", header.Name, header.Mode, entry.mode.Perm())
		}
		data, readErr := io.ReadAll(archive)
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(data, entry.data) {
			return fmt.Errorf("entry %q content differs", header.Name)
		}
		delete(expected, header.Name)
	}
	if len(expected) != 0 {
		return fmt.Errorf("archive is missing %d entries", len(expected))
	}
	return nil
}

func verifyZip(filePath string, entries []archiveEntry) error {
	archive, err := zip.OpenReader(filePath)
	if err != nil {
		return err
	}
	defer archive.Close()

	expected := expectedEntries(entries)
	for _, file := range archive.File {
		entry, ok := expected[file.Name]
		if !ok {
			return fmt.Errorf("unexpected archive entry %q", file.Name)
		}
		if file.Mode().Perm() != entry.mode.Perm() {
			return fmt.Errorf("entry %q mode = %o, want %o", file.Name, file.Mode().Perm(), entry.mode.Perm())
		}
		reader, openErr := file.Open()
		if openErr != nil {
			return openErr
		}
		data, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if !bytes.Equal(data, entry.data) {
			return fmt.Errorf("entry %q content differs", file.Name)
		}
		delete(expected, file.Name)
	}
	if len(expected) != 0 {
		return fmt.Errorf("archive is missing %d entries", len(expected))
	}
	return nil
}

func writeChecksums(directory string, artifacts []string) error {
	names := append([]string(nil), artifacts...)
	sort.Strings(names)
	var manifest strings.Builder
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return fmt.Errorf("checksum %s: %w", name, err)
		}
		digest := sha256.Sum256(data)
		fmt.Fprintf(&manifest, "%x  %s\n", digest, name)
	}
	if err := os.WriteFile(filepath.Join(directory, checksumFileName), []byte(manifest.String()), 0o644); err != nil {
		return fmt.Errorf("write checksum manifest: %w", err)
	}
	return nil
}

func verifyOutputSet(directory string, artifacts []string) error {
	expected := make(map[string]bool, len(artifacts)+1)
	expected[checksumFileName] = true
	for _, artifact := range artifacts {
		expected[artifact] = true
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("verify output directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !expected[entry.Name()] {
			return fmt.Errorf("unexpected release output %s", entry.Name())
		}
		delete(expected, entry.Name())
	}
	if len(expected) != 0 {
		return fmt.Errorf("release output is missing %d files", len(expected))
	}
	return nil
}
