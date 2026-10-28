package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestVersionFlagPrintsPackageJSONVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--version"}, &stdout, &stderr)

	want := "skyboxctl " + packageVersion(t) + "\n"
	if code != 0 || stdout.String() != want {
		t.Fatalf("run(--version) = %d, %q; want 0, %q", code, stdout.String(), want)
	}
}

func TestNoFlagPrintsUsageAndFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, &stdout, &stderr)

	if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "-version") {
		t.Fatalf("run() = %d, stdout %q, stderr %q; want exit 2 and usage naming -version", code, stdout.String(), stderr.String())
	}
}

func TestUnknownFlagFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--nope"}, &stdout, &stderr)

	if code != 2 || stdout.Len() != 0 {
		t.Fatalf("run(--nope) = %d, stdout %q; want exit 2 and no stdout", code, stdout.String())
	}
}

func packageVersion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../package.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest.Version
}
