package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/richardamare/skybox/internal/sandbox"
)

func TestVersionFlagPrintsPackageJSONVersion(t *testing.T) {
	got := runCLI(t, testDeps(newFakeProvider()), "--version")

	want := "skybox " + packageVersion(t) + "\n"
	if got.code != 0 || got.stdout != want {
		t.Fatalf("--version = %d, %q; want 0, %q", got.code, got.stdout, want)
	}
}

func TestNoCommandPrintsUsageAndFails(t *testing.T) {
	got := runCLI(t, testDeps(newFakeProvider()))

	if got.code != 2 || got.stdout != "" || !strings.Contains(got.stderr, "--version") {
		t.Fatalf("no args = %d, stdout %q, stderr %q; want exit 2 and usage naming --version", got.code, got.stdout, got.stderr)
	}
}

func TestUnknownFlagFails(t *testing.T) {
	got := runCLI(t, testDeps(newFakeProvider()), "--nope")

	if got.code != 2 || got.stdout != "" {
		t.Fatalf("--nope = %d, stdout %q; want exit 2 and no stdout", got.code, got.stdout)
	}
}

func TestUnknownCommandFails(t *testing.T) {
	got := runCLI(t, testDeps(newFakeProvider()), "start", "api-fix")

	if got.code != 2 || !strings.Contains(got.stderr, `unknown command "start"`) {
		t.Fatalf("start = %d, stderr %q; want exit 2 naming the unknown command", got.code, got.stderr)
	}
}

func TestEveryCommandPrintsOneJSONDocument(t *testing.T) {
	running := sandbox.Sandbox{Name: "api-fix", Size: sandbox.Standard, MachineType: "Standard_D4as_v7", State: sandbox.Running}
	for _, args := range [][]string{
		{"up", "web", "--json"},
		{"ls", "--json"},
		{"sleep", "api-fix", "--json"},
		{"wake", "api-fix", "--json"},
		{"down", "api-fix", "--json"},
		{"down", "typo", "--json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			got := runCLI(t, testDeps(newFakeProvider(running)), args...)
			var doc map[string]any
			decodeOne(t, got.stdout, &doc)
		})
	}
}

func TestErrorWithJSONIsADocumentOnStdout(t *testing.T) {
	got := runCLI(t, testDeps(newFakeProvider()), "sleep", "typo", "--json")

	var doc struct{ Error string }
	decodeOne(t, got.stdout, &doc)
	if got.code != 1 || doc.Error != "no sandbox named typo" {
		t.Fatalf("sleep typo --json = %d, %+v; want exit 1 and the error in JSON", got.code, doc)
	}
}

func TestInvalidNameFailsBeforeReachingTheCloud(t *testing.T) {
	d := testDeps(newFakeProvider())
	d.provider = func(context.Context) (sandbox.Provider, error) {
		t.Fatal("the provider was built for an invalid name")
		return nil, nil
	}
	got := runCLI(t, d, "up", "Api_Fix")

	if got.code != 1 || !strings.Contains(got.stderr, `invalid sandbox name "Api_Fix"`) {
		t.Fatalf("up Api_Fix = %d, stderr %q; want exit 1 naming the invalid name", got.code, got.stderr)
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
