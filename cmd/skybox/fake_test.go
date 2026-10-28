package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/richardamare/skybox/internal/cloudflare"
	"github.com/richardamare/skybox/internal/sandbox"
	"github.com/richardamare/skybox/internal/tailscale"
	"github.com/richardamare/skybox/internal/toolkit"
)

// fakeProvider keeps sandboxes in memory and gives each size the machine type the Azure provider uses.
type fakeProvider struct {
	sandboxes map[string]sandbox.Sandbox
	capacity  error
	createErr error
	created   []sandbox.Spec
}

var fakeMachines = map[sandbox.Size]string{
	sandbox.Light:    "Standard_D2als_v7",
	sandbox.Standard: "Standard_D4as_v7",
	sandbox.Large:    "Standard_D8as_v7",
}

func newFakeProvider(existing ...sandbox.Sandbox) *fakeProvider {
	p := &fakeProvider{sandboxes: map[string]sandbox.Sandbox{}}
	for _, s := range existing {
		p.sandboxes[s.Name] = s
	}
	return p
}

func (p *fakeProvider) Get(_ context.Context, name string) (sandbox.Sandbox, error) {
	s, ok := p.sandboxes[name]
	if !ok {
		return sandbox.Sandbox{}, &sandbox.NotFoundError{Name: name}
	}
	return s, nil
}

func (p *fakeProvider) List(context.Context) ([]sandbox.Sandbox, error) {
	var all []sandbox.Sandbox
	for _, s := range p.sandboxes {
		all = append(all, s)
	}
	return all, nil
}

func (p *fakeProvider) CheckCapacity(context.Context, sandbox.Size) error { return p.capacity }

func (p *fakeProvider) Create(_ context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	p.created = append(p.created, spec)
	if p.createErr != nil {
		return sandbox.Sandbox{}, p.createErr
	}
	s := sandbox.Sandbox{Name: spec.Name, Size: spec.Size, MachineType: fakeMachines[spec.Size], State: sandbox.Running, Labels: spec.Labels}
	p.sandboxes[spec.Name] = s
	return s, nil
}

func (p *fakeProvider) setState(ctx context.Context, name string, state sandbox.State) error {
	s, err := p.Get(ctx, name)
	if err != nil {
		return err
	}
	s.State = state
	p.sandboxes[name] = s
	return nil
}

func (p *fakeProvider) Sleep(ctx context.Context, name string) error {
	return p.setState(ctx, name, sandbox.Asleep)
}

func (p *fakeProvider) Wake(ctx context.Context, name string) error {
	return p.setState(ctx, name, sandbox.Running)
}

func (p *fakeProvider) Delete(ctx context.Context, name string) error {
	if _, err := p.Get(ctx, name); err != nil {
		return err
	}
	delete(p.sandboxes, name)
	return nil
}

// fakeTunnels keeps tunnels and DNS records in memory, each record under its hostname with the sandbox it names.
// A tunnel turns healthy after healthyAfter checks.
type fakeTunnels struct {
	tunnels      map[string]bool
	records      map[string]string
	createErr    error
	healthyAfter int
	checks       int
}

func newFakeTunnels() *fakeTunnels {
	return &fakeTunnels{tunnels: map[string]bool{}, records: map[string]string{}}
}

func (f *fakeTunnels) Hostname(name string) string { return name + "-sbx.example.com" }

func (f *fakeTunnels) CreateTunnel(_ context.Context, name string) (cloudflare.Tunnel, error) {
	if f.createErr != nil {
		return cloudflare.Tunnel{}, f.createErr
	}
	id := "tunnel-" + name
	f.tunnels[id] = true
	return cloudflare.Tunnel{ID: id, Token: "token-" + name}, nil
}

func (f *fakeTunnels) Route(context.Context, string, string) error { return nil }

func (f *fakeTunnels) CreateDNSRecords(_ context.Context, sandbox, hostname, _ string) error {
	f.records[hostname] = sandbox
	for _, port := range cloudflare.PreviewPorts {
		f.records[cloudflare.PreviewHostname(hostname, port)] = sandbox
	}
	return nil
}

func (f *fakeTunnels) TunnelHealthy(_ context.Context, id string) (bool, error) {
	if !f.tunnels[id] {
		return false, errors.New("no such tunnel")
	}
	f.checks++
	return f.checks > f.healthyAfter, nil
}

func (f *fakeTunnels) DeleteDNSRecords(_ context.Context, sandbox string) error {
	for hostname, owner := range f.records {
		if owner == sandbox {
			delete(f.records, hostname)
		}
	}
	return nil
}

func (f *fakeTunnels) DeleteTunnel(_ context.Context, id string) error {
	delete(f.tunnels, id)
	return nil
}

// fakeTailnet keeps auth keys and devices in memory. A sandbox whose auth key is live joins the tailnet, spending the
// key, the first time its devices are listed, unless neverJoins is set.
type fakeTailnet struct {
	keys       map[string]tailscale.AuthKey
	devices    map[string]tailscale.Device
	mintErr    error
	neverJoins bool
	minted     []string
	revoked    []string
}

func newFakeTailnet() *fakeTailnet {
	return &fakeTailnet{keys: map[string]tailscale.AuthKey{}, devices: map[string]tailscale.Device{}}
}

func (f *fakeTailnet) MintAuthKey(_ context.Context, sandbox string) (tailscale.AuthKey, error) {
	if f.mintErr != nil {
		return tailscale.AuthKey{}, f.mintErr
	}
	key := tailscale.AuthKey{ID: fmt.Sprintf("k%d", len(f.minted)+1), Key: "tskey-auth-" + sandbox}
	f.keys[sandbox] = key
	f.minted = append(f.minted, sandbox)
	return key, nil
}

func (f *fakeTailnet) RevokeAuthKey(_ context.Context, id string) error {
	for sandbox, key := range f.keys {
		if key.ID == id {
			delete(f.keys, sandbox)
		}
	}
	f.revoked = append(f.revoked, id)
	return nil
}

func (f *fakeTailnet) Devices(_ context.Context, sandbox string) ([]tailscale.Device, error) {
	if _, live := f.keys[sandbox]; live && !f.neverJoins {
		delete(f.keys, sandbox)
		id := "node-" + sandbox
		f.devices[id] = tailscale.Device{ID: id, Hostname: sandbox, Name: sandbox + ".tail1234.ts.net", Tags: []string{tailscale.Tag}}
	}
	var found []tailscale.Device
	for _, d := range f.devices {
		if d.Hostname == sandbox {
			found = append(found, d)
		}
	}
	return found, nil
}

func (f *fakeTailnet) DeleteDevices(_ context.Context, sandbox string) error {
	for id, d := range f.devices {
		if d.Hostname == sandbox {
			delete(f.devices, id)
		}
	}
	return nil
}

// fakeClaude stands in for Claude Code on a sandbox. It logs every call, keeps the installed plugins one per line,
// and reports agent-config's version as the commit of the directory its marketplace was added from, as Claude Code
// does for a plugin whose manifest names no version.
const fakeClaude = `#!/bin/bash
set -euo pipefail
state="$HOME/.fake-claude"
mkdir -p "$state"
touch "$state/installed"
echo "$*" >>"$state/calls"
case "$1 $2" in
"plugin marketplace")
  if [[ $3 == add && -d $4 ]]; then echo "$4" >"$state/agent-config-source"; fi
  ;;
"plugin install")
  grep -qxF "$3" "$state/installed" || echo "$3" >>"$state/installed"
  ;;
"plugin update")
  grep -qxF "$3" "$state/installed"
  ;;
"plugin uninstall")
  grep -vxF "$3" "$state/installed" >"$state/next" || true
  mv "$state/next" "$state/installed"
  ;;
"plugin list")
  sep=
  printf '['
  while read -r id; do
    version=1.0.0
    if [[ $id == agent-config@agent-config ]]; then
      version=$(git -C "$(cat "$state/agent-config-source")" rev-parse --short=12 HEAD)
    fi
    printf '%s{"id":"%s","version":"%s","scope":"user","enabled":true}' "$sep" "$id" "$version"
    sep=,
  done <"$state/installed"
  printf ']\n'
  ;;
esac
`

// fakeSandboxes are the agent accounts' homes behind each ssh alias. ssh runs the command there with bash, as the
// sandbox's login shell would, with the fake claude first on the PATH the toolkit script sets.
type fakeSandboxes struct {
	t     *testing.T
	root  string
	calls []string
	// beforeRun checks the Mac's state at the moment ssh starts.
	beforeRun func(alias string)
}

// home returns the agent account's home behind alias, with Claude Code in it.
func (f *fakeSandboxes) home(alias string) string {
	f.t.Helper()
	home := filepath.Join(f.root, alias)
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o755); err != nil {
		f.t.Fatal(err)
	}
	return home
}

func (f *fakeSandboxes) ssh(ctx context.Context, alias, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	f.calls = append(f.calls, alias)
	if f.beforeRun != nil {
		f.beforeRun(alias)
	}
	home := f.home(alias)
	shell := exec.CommandContext(ctx, "bash", "-c", command)
	shell.Dir = home
	shell.Env = append(os.Environ(), "HOME="+home)
	shell.Stdin, shell.Stdout, shell.Stderr = stdin, stdout, stderr
	return shell.Run()
}

// installed reads the plugins Claude Code holds behind alias.
func (f *fakeSandboxes) installed(alias string) []string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.home(alias), ".fake-claude", "installed"))
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.Fields(string(data))
}

// claudeCalls reads every claude command line the sandbox behind alias ran.
func (f *fakeSandboxes) claudeCalls(alias string) []string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.home(alias), ".fake-claude", "calls"))
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// withToolkit gives the Mac an agent-config clone at d.agentConfig with one commit, and points ssh at fake sandboxes.
func withToolkit(t *testing.T, d *deps) *fakeSandboxes {
	t.Helper()
	writeFiles(t, d.agentConfig, map[string]string{
		".claude-plugin/marketplace.json":   `{"name":"agent-config","plugins":[{"name":"agent-config","source":"./plugin"}]}`,
		"plugin/.claude-plugin/plugin.json": `{"name":"agent-config"}`,
		"plugin/skills/review/SKILL.md":     "---\nname: review\n---\nReview the diff.\n",
		"plugin/hooks/format.sh":            "#!/bin/sh\n",
		toolkit.Instructions:                "# Rules\n\nEvery agent follows these. ✓",
	})
	if err := os.Chmod(filepath.Join(d.agentConfig, "plugin/hooks/format.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(toolkit.Instructions, filepath.Join(d.agentConfig, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	git(t, d.agentConfig, "init", "-q")
	commitAll(t, d.agentConfig, "first")
	f := &fakeSandboxes{t: t, root: t.TempDir()}
	d.ssh = f.ssh
	return f
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commitAll commits every change in the clone and returns the short commit Claude Code reports as the version.
func commitAll(t *testing.T, dir, message string) string {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false",
		"-c", "core.hooksPath=/dev/null", "commit", "-q", "-m", message)
	return git(t, dir, "rev-parse", "--short=12", "HEAD")
}

// testDeps runs every command against fakes, with ~/.ssh in a temporary directory.
func testDeps(t *testing.T, p *fakeProvider) deps {
	t.Helper()
	cf := newFakeTunnels()
	ts := newFakeTailnet()
	home := t.TempDir()
	return deps{
		provider: func(context.Context) (sandbox.Provider, error) { return p, nil },
		sshKeys: func(context.Context) ([]string, error) {
			return []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFake test"}, nil
		},
		tunnels: func(context.Context) (tunnels, error) { return cf, nil },
		tailnet: func(context.Context) (tailnet, error) { return ts, nil },
		connect: func(string) error {
			t.Fatal("proxy connected")
			return nil
		},
		answers: func(context.Context, string) bool { return true },
		ssh: func(context.Context, string, string, io.Reader, io.Writer, io.Writer) error {
			t.Fatal("ssh ran")
			return nil
		},
		sshConfig:     filepath.Join(home, ".ssh", "config"),
		knownHosts:    filepath.Join(home, ".ssh", "skybox_known_hosts"),
		agentConfig:   filepath.Join(home, ".claude", "plugins", "marketplaces", "agent-config"),
		executable:    "/usr/local/bin/skybox",
		tunnelTimeout: time.Second,
		joinTimeout:   10 * time.Millisecond,
		pollInterval:  time.Millisecond,
	}
}

func runningSandbox(name string) sandbox.Sandbox {
	return sandbox.Sandbox{
		Name: name, Size: sandbox.Standard, MachineType: "Standard_D4as_v7", State: sandbox.Running,
		Labels: map[string]string{
			labelTunnelID: "tunnel-skybox-" + name, labelHostname: name + "-sbx.example.com",
		},
	}
}

type outcome struct {
	code           int
	stdout, stderr string
}

func runCLI(t *testing.T, d deps, args ...string) outcome {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := d.execute(context.Background(), args, &stdout, &stderr)
	return outcome{code, stdout.String(), stderr.String()}
}

// decodeOne fails unless stdout holds exactly one JSON document.
func decodeOne(t *testing.T, stdout string, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewBufferString(stdout))
	if err := dec.Decode(v); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if dec.More() {
		t.Fatalf("stdout holds more than one JSON document:\n%s", stdout)
	}
}

func tunnelsOf(t *testing.T, d deps) *fakeTunnels {
	t.Helper()
	cf, _ := d.tunnels(context.Background())
	return cf.(*fakeTunnels)
}

func tailnetOf(t *testing.T, d deps) *fakeTailnet {
	t.Helper()
	ts, _ := d.tailnet(context.Background())
	return ts.(*fakeTailnet)
}
