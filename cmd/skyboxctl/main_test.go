package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/richardamare/skybox/internal/control"
)

func TestVersionFlagPrintsPackageJSONVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"--version"}, &stdout, &stderr, unreachable(t))

	want := "skyboxctl " + packageVersion(t) + "\n"
	if code != 0 || stdout.String() != want {
		t.Fatalf("run(--version) = %d, %q; want 0, %q", code, stdout.String(), want)
	}
}

func TestNoFlagPrintsUsageAndFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), nil, &stdout, &stderr, unreachable(t))

	if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "-version") {
		t.Fatalf("run() = %d, stdout %q, stderr %q; want exit 2 and usage naming -version", code, stdout.String(), stderr.String())
	}
}

func TestUnknownFlagFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"--nope"}, &stdout, &stderr, unreachable(t))

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

func unreachable(t *testing.T) connector {
	return func(context.Context) (control.Cloud, control.Edge, error) {
		t.Fatal("connected to the clouds")
		return nil, nil, nil
	}
}

func TestReconcileRejectsAnUnknownFlagBeforeItConnects(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"reconcile", "--force"}, &stdout, &stderr, unreachable(t))

	if code != 2 || !strings.Contains(stderr.String(), "-enforce") {
		t.Fatalf("run(reconcile --force) = %d, stderr %q; want exit 2 and usage naming -enforce", code, stderr.String())
	}
}

func TestReconcileFailsWhenItCannotConnect(t *testing.T) {
	var stdout, stderr bytes.Buffer
	failing := func(context.Context) (control.Cloud, control.Edge, error) {
		return nil, nil, errors.New("set CLOUDFLARE_API_TOKEN")
	}

	code := run(t.Context(), []string{"reconcile"}, &stdout, &stderr, failing)

	if code != 1 || !strings.Contains(stdout.String(), `"error":"set CLOUDFLARE_API_TOKEN"`) {
		t.Fatalf("run(reconcile) = %d, stdout %q; want exit 1 and the error logged as JSON", code, stdout.String())
	}
}
