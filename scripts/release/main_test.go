package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateVersion(t *testing.T) {
	tests := map[string]bool{
		"v0.1.0":                            true,
		"v1.2.3-rc.1":                       true,
		"v1.2.3-0":                          true,
		"v1.2.3+build.7":                    false,
		"v1.2.3-rc.1+x.2":                   false,
		"1.2.3":                             false,
		"v01.2.3":                           false,
		"v1.2":                              false,
		"v1.2.3/unsafe":                     false,
		"v1.2.3-":                           false,
		"v1.2.3-01":                         false,
		"v1.2.3-" + strings.Repeat("a", 58): false,
	}
	for version, valid := range tests {
		t.Run(version, func(t *testing.T) {
			err := validateVersion(version)
			if valid && err != nil {
				t.Fatalf("valid version rejected: %v", err)
			}
			if !valid && err == nil {
				t.Fatal("invalid version accepted")
			}
		})
	}
}

func TestReleaseEnvironmentAllowsRuntimePathsAndControlsBuildSettings(t *testing.T) {
	got := releaseEnvironment(
		[]string{
			"Path=/bin",
			"HOME=/home/test",
			"goos=old",
			"GOARCH=old",
			"GOFIPS140=latest",
			"KEEP=value",
		},
		map[string]string{
			"CGO_ENABLED": "0",
			"GOARCH":      "arm64",
			"GOFIPS140":   "off",
			"GOOS":        "linux",
		},
	)
	want := []string{
		"Path=/bin",
		"HOME=/home/test",
		"CGO_ENABLED=0",
		"GOARCH=arm64",
		"GOFIPS140=off",
		"GOOS=linux",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("environment = %q, want %q", got, want)
	}
}

func TestVerifyArchiveRejectsUnknownExtension(t *testing.T) {
	err := verifyArchive(filepath.Join(t.TempDir(), "release.bin"), nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported archive extension") {
		t.Fatalf("error = %v, want unsupported-extension error", err)
	}
}

func TestPrepareEmptyDirectoryRejectsStaleOutput(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "dist")
	if err := prepareEmptyDirectory(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "stale"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareEmptyDirectory(directory); err == nil {
		t.Fatal("non-empty release directory was accepted")
	}
}

func TestArchivesAreDeterministicAndRoundTripExactBytes(t *testing.T) {
	entries := []archiveEntry{
		{name: "runprint_0.1.0_linux_amd64/runprint", mode: 0o755, data: []byte{0x00, 0xff, 0x01}},
		{name: "runprint_0.1.0_linux_amd64/LICENSE", mode: 0o644, data: []byte("license\n")},
		{name: "runprint_0.1.0_linux_amd64/README.md", mode: 0o644, data: []byte("readme\n")},
	}
	for _, extension := range []string{".tar.gz", ".zip"} {
		t.Run(extension, func(t *testing.T) {
			directory := t.TempDir()
			first := filepath.Join(directory, "first"+extension)
			second := filepath.Join(directory, "second"+extension)
			if err := writeArchive(first, entries); err != nil {
				t.Fatal(err)
			}
			if err := writeArchive(second, entries); err != nil {
				t.Fatal(err)
			}
			firstBytes, err := os.ReadFile(first)
			if err != nil {
				t.Fatal(err)
			}
			secondBytes, err := os.ReadFile(second)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(firstBytes, secondBytes) {
				t.Fatal("archives built from identical inputs differ")
			}
			if err := verifyArchive(first, entries); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestChecksumsAndOutputSetAreSortedAndComplete(t *testing.T) {
	directory := t.TempDir()
	artifacts := []string{"z.zip", "a.tar.gz"}
	if err := os.WriteFile(filepath.Join(directory, artifacts[0]), []byte("z"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, artifacts[1]), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeChecksums(directory, artifacts); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(directory, checksumFileName))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(manifest)), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], "  a.tar.gz") || !strings.HasSuffix(lines[1], "  z.zip") {
		t.Fatalf("checksum manifest is not sorted: %q", manifest)
	}
	if err := verifyOutputSet(directory, artifacts); err != nil {
		t.Fatal(err)
	}
}
