package main

import (
	"strings"
	"testing"

	"github.com/richardamare/skybox/internal/sandbox"
)

func TestProxyConnectsARunningSandbox(t *testing.T) {
	d := testDeps(t, newFakeProvider(runningSandbox("api-fix")))
	var connected string
	d.connect = func(hostname string) error {
		connected = hostname
		return nil
	}
	got := runCLI(t, d, "proxy", "api-fix")

	if got.code != 0 || connected != "api-fix-sbx.example.com" {
		t.Fatalf("proxy = %d, connected to %q", got.code, connected)
	}
}

func TestProxyRefusesASleepingSandboxWithoutWakingIt(t *testing.T) {
	asleep := runningSandbox("api-fix")
	asleep.State = sandbox.Asleep
	p := newFakeProvider(asleep)
	got := runCLI(t, testDeps(t, p), "proxy", "api-fix")

	if got.code != 1 || !strings.Contains(got.stderr, "api-fix is asleep, run skybox wake api-fix") {
		t.Fatalf("proxy = %d, stderr %q; want exit 1 with the asleep message", got.code, got.stderr)
	}
	if p.sandboxes["api-fix"].State != sandbox.Asleep {
		t.Fatal("proxy woke the sandbox")
	}
	if strings.Contains(got.stderr, "Usage") {
		t.Fatal("proxy printed usage, which T3 Code would show as the error")
	}
}

func TestProxyNamesAMissingSandboxWithoutCreatingIt(t *testing.T) {
	p := newFakeProvider()
	got := runCLI(t, testDeps(t, p), "proxy", "typo")

	if got.code != 1 || !strings.Contains(got.stderr, "no sandbox named typo") || len(p.created) != 0 {
		t.Fatalf("proxy = %d, stderr %q; want exit 1 naming typo and nothing created", got.code, got.stderr)
	}
}

func TestProxyTakesTheAliasFromTheCatchAll(t *testing.T) {
	p := newFakeProvider()
	got := runCLI(t, testDeps(t, p), "proxy", "--alias", "skybox-typo")

	if got.code != 1 || !strings.Contains(got.stderr, "no sandbox named typo") {
		t.Fatalf("proxy --alias skybox-typo = %d, stderr %q; want exit 1 naming typo", got.code, got.stderr)
	}
}

func TestProxyFindsHomebrewTools(t *testing.T) {
	got := withToolPath("/usr/bin:/bin")
	if got != "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin" {
		t.Fatalf("withToolPath = %q", got)
	}
	if again := withToolPath(got); again != got {
		t.Fatalf("withToolPath added a directory twice: %q", again)
	}
}
