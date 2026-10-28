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
	if spec.TunnelToken != "token-skybox-api-fix" || spec.Labels[labelTunnelID] != "tunnel-skybox-api-fix" || spec.Labels[labelHostname] != "api-fix-sbx.example.com" {
		t.Fatalf("spec = %+v; want the tunnel token, the tunnel id and the hostname", spec)
	}
}

func TestUpGivesEveryPreviewPortADNSRecord(t *testing.T) {
	d := testDeps(t, newFakeProvider())
	cf := tunnelsOf(t, d)
	got := runCLI(t, d, "up", "api-fix", "--json")

	var doc sandboxResult
	decodeOne(t, got.stdout, &doc)
	if got.code != 0 || len(doc.Previews) != len(cloudflare.PreviewPorts) || doc.Previews[0] != "https://3000-api-fix-sbx.example.com" {
		t.Fatalf("up --json = %d, previews %v; want https://3000-api-fix-sbx.example.com first", got.code, doc.Previews)
	}
	for _, port := range cloudflare.PreviewPorts {
		if host := cloudflare.PreviewHostname("api-fix-sbx.example.com", port); cf.records[host] != "api-fix" {
			t.Errorf("no DNS record for %s", host)
		}
	}
}

func TestUpPrintsWhereThePreviewsAre(t *testing.T) {
	got := runCLI(t, testDeps(t, newFakeProvider()), "up", "api-fix")

	if got.code != 0 || !strings.Contains(got.stdout, "preview ports 3000 to 3009 at: https://<port>-api-fix-sbx.example.com\n") {
		t.Fatalf("up = %d, stdout %q; want the preview hostname pattern", got.code, got.stdout)
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

func TestDownRemovesTheTunnelTheRecordsAndTheAlias(t *testing.T) {
	p := newFakeProvider()
	d := testDeps(t, p)
	cf := tunnelsOf(t, d)
	if got := runCLI(t, d, "up", "api-fix"); got.code != 0 {
		t.Fatalf("up = %d, %s", got.code, got.stderr)
	}
	if got := runCLI(t, d, "up", "web"); got.code != 0 {
		t.Fatalf("up web = %d, %s", got.code, got.stderr)
	}
	if err := os.WriteFile(d.knownHosts, []byte("skybox-api-fix ssh-ed25519 AAAA\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := runCLI(t, d, "down", "api-fix")

	config, _ := os.ReadFile(d.sshConfig)
	if !strings.Contains(string(config), "Host skybox-*") {
		t.Error("down removed the catch-all, which no sandbox owns")
	}
	for host, owner := range cf.records {
		if owner != "web" {
			t.Errorf("down api-fix left %s, which belongs to %s", host, owner)
		}
	}
	if len(cf.records) != 1+len(cloudflare.PreviewPorts) {
		t.Errorf("down api-fix left %d records of web; want all %d", len(cf.records), 1+len(cloudflare.PreviewPorts))
	}
	known, _ := os.ReadFile(d.knownHosts)
	if got.code != 0 || len(cf.tunnels) != 1 || strings.Contains(string(config), "skybox-api-fix") || len(known) != 0 {
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
