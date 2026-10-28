package main

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/richardamare/skybox/internal/onepassword"
	"github.com/richardamare/skybox/internal/tailscale"
)

func TestUpJoinsTheSandboxToTheTailnetWithItsOwnAuthKey(t *testing.T) {
	p := newFakeProvider()
	d := testDeps(t, p)
	ts := tailnetOf(t, d)
	got := runCLI(t, d, "up", "api-fix", "--json")

	var doc sandboxResult
	decodeOne(t, got.stdout, &doc)
	if got.code != 0 || doc.TailnetURL != "https://api-fix.tail1234.ts.net" {
		t.Fatalf("up --json = %d, %+v, stderr %q; want the sandbox's tailnet address", got.code, doc, got.stderr)
	}
	if p.created[0].TailnetAuthKey != "tskey-auth-api-fix" {
		t.Fatalf("spec auth key = %q; want the key minted for api-fix", p.created[0].TailnetAuthKey)
	}
	device := ts.devices["node-api-fix"]
	if !slices.Equal(device.Tags, []string{tailscale.Tag}) {
		t.Fatalf("device = %+v; want api-fix tagged %s", device, tailscale.Tag)
	}
}

func TestUpMintsOneAuthKeyPerSandbox(t *testing.T) {
	p := newFakeProvider()
	d := testDeps(t, p)
	ts := tailnetOf(t, d)
	runCLI(t, d, "up", "api-fix")
	runCLI(t, d, "up", "web")

	if !slices.Equal(ts.minted, []string{"api-fix", "web"}) || p.created[0].TailnetAuthKey == p.created[1].TailnetAuthKey {
		t.Fatalf("minted %v, specs %+v; want a key of its own for each sandbox", ts.minted, p.created)
	}
}

func TestUpPrintsWhereT3CodeLinksTheSandbox(t *testing.T) {
	got := runCLI(t, testDeps(t, newFakeProvider()), "up", "api-fix")

	if got.code != 0 || !strings.Contains(got.stdout, "link in T3 Code at: https://api-fix.tail1234.ts.net\n") {
		t.Fatalf("up = %d, stdout %q; want the tailnet address", got.code, got.stdout)
	}
}

func TestUpRemovesALeftoverDeviceOfTheSameName(t *testing.T) {
	d := testDeps(t, newFakeProvider())
	ts := tailnetOf(t, d)
	ts.devices["node-old"] = tailscale.Device{ID: "node-old", Hostname: "api-fix", Name: "api-fix.tail1234.ts.net", Tags: []string{tailscale.Tag}}
	got := runCLI(t, d, "up", "api-fix")

	if _, ok := ts.devices["node-old"]; got.code != 0 || ok || len(ts.devices) != 1 {
		t.Fatalf("up = %d, devices %v; want the leftover gone and the new device alone", got.code, ts.devices)
	}
}

func TestUpFailsWhenNoAuthKeyCanBeMintedAndCreatesNothing(t *testing.T) {
	p := newFakeProvider()
	d := testDeps(t, p)
	cf := tunnelsOf(t, d)
	tailnetOf(t, d).mintErr = &tailscale.ScopeError{Action: "create an auth key", Scope: "auth_keys", Detail: "forbidden"}
	got := runCLI(t, d, "up", "api-fix")

	if got.code != 1 || !strings.Contains(got.stderr, "mint the tailnet auth key for api-fix") || !strings.Contains(got.stderr, "auth_keys") {
		t.Fatalf("up = %d, stderr %q; want exit 1 naming the missing scope", got.code, got.stderr)
	}
	if len(p.created) != 0 || len(cf.tunnels) != 0 || len(cf.records) != 0 {
		t.Fatalf("created %v, tunnels %v, records %v; want nothing", p.created, cf.tunnels, cf.records)
	}
	if _, err := os.Stat(d.sshConfig); !os.IsNotExist(err) {
		t.Fatal("a failed up wrote an ssh alias")
	}
}

func TestUpNamesTheTailscaleItemOpCannotRead(t *testing.T) {
	p := newFakeProvider()
	d := testDeps(t, p)
	cf := tunnelsOf(t, d)
	d.tailnet = func(context.Context) (tailnet, error) {
		return nil, &onepassword.ReadError{Item: "Tailscale OAuth client", Detail: "account is not signed in"}
	}
	got := runCLI(t, d, "up", "api-fix")

	if got.code != 1 || !strings.Contains(got.stderr, `"Tailscale OAuth client"`) || len(p.created) != 0 || len(cf.tunnels) != 0 {
		t.Fatalf("up = %d, stderr %q, created %d, tunnels %v; want exit 1 naming the item and nothing created", got.code, got.stderr, len(p.created), cf.tunnels)
	}
}

func TestUpRevokesTheAuthKeyWhenTheSandboxCannotBeCreated(t *testing.T) {
	p := newFakeProvider()
	p.createErr = os.ErrDeadlineExceeded
	d := testDeps(t, p)
	ts := tailnetOf(t, d)
	got := runCLI(t, d, "up", "api-fix")

	if got.code != 1 || !slices.Equal(ts.revoked, []string{"k1"}) || len(ts.keys) != 0 {
		t.Fatalf("up = %d, revoked %v, live keys %v; want exit 1 and the key revoked", got.code, ts.revoked, ts.keys)
	}
}

func TestUpRevokesTheAuthKeyWhenTheTunnelCannotBeCreated(t *testing.T) {
	d := testDeps(t, newFakeProvider())
	tunnelsOf(t, d).createErr = os.ErrPermission
	ts := tailnetOf(t, d)
	got := runCLI(t, d, "up", "api-fix")

	if got.code != 1 || !slices.Equal(ts.revoked, []string{"k1"}) {
		t.Fatalf("up = %d, revoked %v; want exit 1 and the key revoked", got.code, ts.revoked)
	}
}

func TestUpFailsWhenTheSandboxNeverJoinsTheTailnet(t *testing.T) {
	d := testDeps(t, newFakeProvider())
	tailnetOf(t, d).neverJoins = true
	got := runCLI(t, d, "up", "api-fix")

	if got.code != 1 || !strings.Contains(got.stderr, "did not join the tailnet") || !strings.Contains(got.stderr, "skybox down api-fix") {
		t.Fatalf("up = %d, stderr %q; want exit 1 saying the sandbox never joined", got.code, got.stderr)
	}
}

func TestSleepAndWakeKeepTheSandboxsDevice(t *testing.T) {
	p := newFakeProvider()
	d := testDeps(t, p)
	ts := tailnetOf(t, d)
	if got := runCLI(t, d, "up", "api-fix"); got.code != 0 {
		t.Fatalf("up = %d, %s", got.code, got.stderr)
	}
	before := ts.devices["node-api-fix"]

	for _, command := range []string{"sleep", "wake"} {
		if got := runCLI(t, d, command, "api-fix"); got.code != 0 {
			t.Fatalf("%s = %d, %s", command, got.code, got.stderr)
		}
	}
	after, ok := ts.devices["node-api-fix"]
	if !ok || after.Name != before.Name || len(ts.minted) != 1 || len(ts.revoked) != 0 {
		t.Fatalf("after sleep and wake: device %+v (was %+v), minted %v, revoked %v; want the same device and no new key",
			after, before, ts.minted, ts.revoked)
	}
}

func TestDownRemovesTheSandboxsDeviceAlone(t *testing.T) {
	p := newFakeProvider()
	d := testDeps(t, p)
	ts := tailnetOf(t, d)
	for _, name := range []string{"api-fix", "web"} {
		if got := runCLI(t, d, "up", name); got.code != 0 {
			t.Fatalf("up %s = %d, %s", name, got.code, got.stderr)
		}
	}
	got := runCLI(t, d, "down", "api-fix")

	if _, gone := ts.devices["node-api-fix"]; got.code != 0 || gone {
		t.Fatalf("down = %d, devices %v; want api-fix's device gone", got.code, ts.devices)
	}
	if _, kept := ts.devices["node-web"]; !kept {
		t.Fatalf("down api-fix removed web's device: %v", ts.devices)
	}
}

func TestDownNamesTheTailscaleItemBeforeDeletingAnything(t *testing.T) {
	p := newFakeProvider(runningSandbox("api-fix"))
	d := testDeps(t, p)
	d.tailnet = func(context.Context) (tailnet, error) {
		return nil, &onepassword.ReadError{Item: "Tailscale OAuth client", Detail: "account is not signed in"}
	}
	got := runCLI(t, d, "down", "api-fix")

	if _, kept := p.sandboxes["api-fix"]; got.code != 1 || !strings.Contains(got.stderr, `"Tailscale OAuth client"`) || !kept {
		t.Fatalf("down = %d, stderr %q, sandbox kept %v; want exit 1 naming the item and the sandbox untouched", got.code, got.stderr, kept)
	}
}
