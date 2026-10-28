package main

import (
	"strings"
	"testing"

	"github.com/richardamare/skybox/internal/sandbox"
)

func TestUpJSONReportsTheNewSandbox(t *testing.T) {
	got := runCLI(t, testDeps(newFakeProvider()), "up", "api-fix", "--json")

	var doc sandbox.Sandbox
	decodeOne(t, got.stdout, &doc)
	if got.code != 0 || doc.Name != "api-fix" || doc.State != sandbox.Running {
		t.Fatalf("up api-fix --json = %d, %+v; want exit 0 and a running api-fix", got.code, doc)
	}
}

func TestUpPicksTheMachineOfEachSize(t *testing.T) {
	for _, tc := range []struct {
		args        []string
		size        sandbox.Size
		machineType string
	}{
		{[]string{"up", "api-fix"}, sandbox.Standard, "Standard_D4as_v7"},
		{[]string{"up", "api-fix", "--size", "light"}, sandbox.Light, "Standard_D2als_v7"},
		{[]string{"up", "--size=large", "api-fix"}, sandbox.Large, "Standard_D8as_v7"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			p := newFakeProvider()
			got := runCLI(t, testDeps(p), tc.args...)

			if got.code != 0 || len(p.created) != 1 || p.created[0].Size != tc.size {
				t.Fatalf("%v = %d, created %+v; want one %s sandbox", tc.args, got.code, p.created, tc.size)
			}
			if !strings.Contains(got.stdout, tc.machineType) {
				t.Fatalf("%v printed %q; want it to name %s", tc.args, got.stdout, tc.machineType)
			}
		})
	}
}

func TestUpInstallsTheAgentsSSHKeys(t *testing.T) {
	p := newFakeProvider()
	runCLI(t, testDeps(p), "up", "api-fix")

	if len(p.created) != 1 || len(p.created[0].SSHKeys) != 1 {
		t.Fatalf("created %+v; want the agent's one key in the spec", p.created)
	}
}

func TestUpFailsWhenTheSandboxExists(t *testing.T) {
	p := newFakeProvider(sandbox.Sandbox{Name: "api-fix", Size: sandbox.Light, State: sandbox.Asleep})
	got := runCLI(t, testDeps(p), "up", "api-fix")

	if got.code != 1 || !strings.Contains(got.stderr, "sandbox api-fix already exists (light, asleep)") || len(p.created) != 0 {
		t.Fatalf("up api-fix = %d, stderr %q, created %d; want exit 1 naming the existing sandbox", got.code, got.stderr, len(p.created))
	}
}

func TestUpRejectsAnUnknownSize(t *testing.T) {
	p := newFakeProvider()
	got := runCLI(t, testDeps(p), "up", "api-fix", "--size", "huge")

	if got.code != 1 || !strings.Contains(got.stderr, "light, standard or large") || len(p.created) != 0 {
		t.Fatalf("up --size huge = %d, stderr %q; want exit 1 listing light, standard and large", got.code, got.stderr)
	}
}

func TestUpFailsNamingTheFamilyWithoutQuota(t *testing.T) {
	p := newFakeProvider()
	p.capacity = &sandbox.CapacityError{Size: sandbox.Standard, Region: "germanywestcentral", Family: "StandardDasv7Family", Free: 2, Need: 4}
	got := runCLI(t, testDeps(p), "up", "api-fix")

	if got.code != 1 || !strings.Contains(got.stderr, "StandardDasv7Family") || len(p.created) != 0 {
		t.Fatalf("up = %d, stderr %q; want exit 1 naming the VM family", got.code, got.stderr)
	}
}
