package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richardamare/skybox/internal/cloudflare"
	"github.com/richardamare/skybox/internal/onepassword"
	"github.com/richardamare/skybox/internal/sandbox"
)

func TestUpReportsTheSSHAliasAndWritesIt(t *testing.T) {
	p := newFakeProvider()
	d := testDeps(t, p)
	got := runCLI(t, d, "up", "api-fix", "--json")

	var doc sandboxResult
	decodeOne(t, got.stdout, &doc)
	if got.code != 0 || doc.SSHAlias != "skybox-api-fix" || doc.Hostname != "api-fix-sbx.example.com" {
		t.Fatalf("up --json = %d, %+v; want the alias skybox-api-fix", got.code, doc)
	}
	config, err := os.ReadFile(d.sshConfig)
	if err != nil || !strings.Contains(string(config), "Host skybox-api-fix\n") ||
		!strings.Contains(string(config), `ProxyCommand "/usr/local/bin/skybox" proxy api-fix`) {
		t.Fatalf("~/.ssh/config = %q, %v; want the alias with skybox proxy", config, err)
	}
	spec := p.created[0]
	if spec.TunnelToken != "token-skybox-api-fix" || spec.Labels[labelTunnelID] == "" || spec.Labels[labelDNSRecord] == "" {
		t.Fatalf("spec = %+v; want the tunnel token and the Cloudflare ids", spec)
	}
}

func TestUpWritesNoKeyToDisk(t *testing.T) {
	d := testDeps(t, newFakeProvider())
	runCLI(t, d, "up", "api-fix")

	var written []string
	_ = filepath.WalkDir(filepath.Dir(filepath.Dir(d.sshConfig)), func(path string, e os.DirEntry, _ error) error {
		if !e.IsDir() {
			written = append(written, filepath.Base(path))
		}
		return nil
	})
	if len(written) != 1 || written[0] != "config" {
		t.Fatalf("up wrote %v; want ~/.ssh/config alone", written)
	}
}

func TestUpWaitsForTheTunnel(t *testing.T) {
	d := testDeps(t, newFakeProvider())
	cf := tunnelsOf(t, d)
	cf.healthyAfter = 3
	got := runCLI(t, d, "up", "api-fix")

	if got.code != 0 || cf.checks != 4 {
		t.Fatalf("up = %d after %d checks; want success on the fourth check", got.code, cf.checks)
	}
}

func TestUpFailsWhenTheTunnelNeverConnects(t *testing.T) {
	d := testDeps(t, newFakeProvider())
	tunnelsOf(t, d).healthyAfter = 1 << 30
	got := runCLI(t, d, "up", "api-fix")

	if got.code != 1 || !strings.Contains(got.stderr, "did not connect its tunnel") {
		t.Fatalf("up = %d, stderr %q; want exit 1 saying the tunnel never connected", got.code, got.stderr)
	}
}

func TestUpRemovesTheTunnelWhenTheSandboxCannotBeCreated(t *testing.T) {
	p := newFakeProvider()
	p.createErr = errors.New("SkuNotAvailable")
	d := testDeps(t, p)
	cf := tunnelsOf(t, d)
	got := runCLI(t, d, "up", "api-fix")

	if got.code != 1 || len(cf.tunnels) != 0 || len(cf.records) != 0 {
		t.Fatalf("up = %d, tunnels %v, records %v; want exit 1 and nothing left in Cloudflare", got.code, cf.tunnels, cf.records)
	}
	if _, err := os.Stat(d.sshConfig); !os.IsNotExist(err) {
		t.Fatal("a failed up wrote an ssh alias")
	}
}

func TestUpNamesTheMissingCloudflarePermission(t *testing.T) {
	p := newFakeProvider()
	d := testDeps(t, p)
	tunnelsOf(t, d).createErr = &cloudflare.PermissionError{Action: "create a tunnel", Permission: "Account › Cloudflare Tunnel › Edit", Detail: "10000 Authentication error"}
	got := runCLI(t, d, "up", "api-fix")

	if got.code != 1 || !strings.Contains(got.stderr, "Account › Cloudflare Tunnel › Edit") || len(p.created) != 0 {
		t.Fatalf("up = %d, stderr %q; want exit 1 naming the permission", got.code, got.stderr)
	}
}

func TestUpNamesThe1PasswordItemOpCannotRead(t *testing.T) {
	p := newFakeProvider()
	d := testDeps(t, p)
	d.tunnels = func(context.Context) (tunnels, error) {
		return nil, &onepassword.ReadError{Item: "Cloudflare API token", Detail: "account is not signed in"}
	}
	got := runCLI(t, d, "up", "api-fix")

	if got.code != 1 || !strings.Contains(got.stderr, `"Cloudflare API token"`) || len(p.created) != 0 {
		t.Fatalf("up = %d, stderr %q; want exit 1 naming the item", got.code, got.stderr)
	}
}

func TestDownRemovesTheTunnelTheRecordAndTheAlias(t *testing.T) {
	p := newFakeProvider()
	d := testDeps(t, p)
	cf := tunnelsOf(t, d)
	if got := runCLI(t, d, "up", "api-fix"); got.code != 0 {
		t.Fatalf("up = %d, %s", got.code, got.stderr)
	}
	if err := os.WriteFile(d.knownHosts, []byte("skybox-api-fix ssh-ed25519 AAAA\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := runCLI(t, d, "down", "api-fix")

	config, _ := os.ReadFile(d.sshConfig)
	if !strings.Contains(string(config), "Host skybox-*") {
		t.Error("down removed the catch-all, which no sandbox owns")
	}
	known, _ := os.ReadFile(d.knownHosts)
	if got.code != 0 || len(cf.tunnels) != 0 || len(cf.records) != 0 || strings.Contains(string(config), "skybox-api-fix") || len(known) != 0 {
		t.Fatalf("down = %d, tunnels %v, records %v, config %q, known_hosts %q", got.code, cf.tunnels, cf.records, config, known)
	}
}

func TestWakeWaitsForTheTunnel(t *testing.T) {
	asleep := runningSandbox("api-fix")
	asleep.State = sandbox.Asleep
	d := testDeps(t, newFakeProvider(asleep))
	cf := tunnelsOf(t, d)
	cf.tunnels[asleep.Labels[labelTunnelID]] = true
	cf.healthyAfter = 2
	got := runCLI(t, d, "wake", "api-fix")

	if got.code != 0 || cf.checks != 3 || !strings.Contains(got.stdout, "ssh skybox-api-fix") {
		t.Fatalf("wake = %d after %d checks, stdout %q", got.code, cf.checks, got.stdout)
	}
}
