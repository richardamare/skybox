package main

import (
	"strings"
	"testing"

	"github.com/richardamare/skybox/internal/sandbox"
)

func TestSleepWakeAndDownFailForAMissingSandbox(t *testing.T) {
	for _, command := range []string{"sleep", "wake", "down"} {
		t.Run(command, func(t *testing.T) {
			got := runCLI(t, testDeps(newFakeProvider()), command, "api-fix")

			if got.code != 1 || !strings.Contains(got.stderr, "no sandbox named api-fix") {
				t.Fatalf("%s api-fix = %d, stderr %q; want exit 1 naming the missing sandbox", command, got.code, got.stderr)
			}
		})
	}
}

func TestSleepThenWake(t *testing.T) {
	p := newFakeProvider(sandbox.Sandbox{Name: "api-fix", Size: sandbox.Standard, State: sandbox.Running})
	d := testDeps(p)

	if got := runCLI(t, d, "sleep", "api-fix"); got.code != 0 || p.sandboxes["api-fix"].State != sandbox.Asleep {
		t.Fatalf("sleep = %d, state %s; want asleep", got.code, p.sandboxes["api-fix"].State)
	}
	if got := runCLI(t, d, "wake", "api-fix"); got.code != 0 || p.sandboxes["api-fix"].State != sandbox.Running {
		t.Fatalf("wake = %d, state %s; want running", got.code, p.sandboxes["api-fix"].State)
	}
}

func TestDownDeletesTheSandbox(t *testing.T) {
	p := newFakeProvider(sandbox.Sandbox{Name: "api-fix", Size: sandbox.Standard, State: sandbox.Running})
	got := runCLI(t, testDeps(p), "down", "api-fix")

	if _, ok := p.sandboxes["api-fix"]; got.code != 0 || ok {
		t.Fatalf("down = %d, still present %v; want it deleted", got.code, ok)
	}
}

func TestLsShowsEachSandboxWithSizeAndState(t *testing.T) {
	p := newFakeProvider(
		sandbox.Sandbox{Name: "web", Size: sandbox.Light, MachineType: "Standard_D2als_v7", State: sandbox.Asleep},
		sandbox.Sandbox{Name: "api-fix", Size: sandbox.Standard, MachineType: "Standard_D4as_v7", State: sandbox.Running},
		sandbox.Sandbox{Name: "broken", Size: sandbox.Large, MachineType: "Standard_D8as_v7", State: sandbox.Failed},
	)
	got := runCLI(t, testDeps(p), "ls")

	lines := strings.Split(strings.TrimSpace(got.stdout), "\n")
	want := [][]string{
		{"NAME", "SIZE", "MACHINE", "STATE"},
		{"api-fix", "standard", "Standard_D4as_v7", "running"},
		{"broken", "large", "Standard_D8as_v7", "failed"},
		{"web", "light", "Standard_D2als_v7", "asleep"},
	}
	if got.code != 0 || len(lines) != len(want) {
		t.Fatalf("ls = %d, %q", got.code, got.stdout)
	}
	for i, fields := range want {
		if strings.Join(strings.Fields(lines[i]), " ") != strings.Join(fields, " ") {
			t.Fatalf("ls line %d = %q; want %v", i, lines[i], fields)
		}
	}
}

func TestLsJSONListsSandboxes(t *testing.T) {
	p := newFakeProvider(sandbox.Sandbox{Name: "api-fix", Size: sandbox.Standard, MachineType: "Standard_D4as_v7", State: sandbox.Asleep})
	got := runCLI(t, testDeps(p), "ls", "--json")

	var doc listResult
	decodeOne(t, got.stdout, &doc)
	if len(doc.Sandboxes) != 1 || doc.Sandboxes[0].State != sandbox.Asleep || doc.Sandboxes[0].Size != sandbox.Standard {
		t.Fatalf("ls --json = %+v", doc)
	}
}

func TestLsJSONWithNoSandboxesIsAnEmptyList(t *testing.T) {
	got := runCLI(t, testDeps(newFakeProvider()), "ls", "--json")

	if !strings.Contains(got.stdout, `"sandboxes": []`) {
		t.Fatalf("ls --json = %q; want an empty sandboxes list", got.stdout)
	}
}
