package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/richardamare/skybox/internal/cloudflare"
	"github.com/richardamare/skybox/internal/sandbox"
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

// testDeps runs every command against fakes, with ~/.ssh in a temporary directory.
func testDeps(t *testing.T, p *fakeProvider) deps {
	t.Helper()
	cf := newFakeTunnels()
	home := t.TempDir()
	return deps{
		provider: func(context.Context) (sandbox.Provider, error) { return p, nil },
		sshKeys: func(context.Context) ([]string, error) {
			return []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFake test"}, nil
		},
		tunnels: func(context.Context) (tunnels, error) { return cf, nil },
		connect: func(string) error {
			t.Fatal("proxy connected")
			return nil
		},
		sshConfig:     filepath.Join(home, ".ssh", "config"),
		knownHosts:    filepath.Join(home, ".ssh", "skybox_known_hosts"),
		executable:    "/usr/local/bin/skybox",
		tunnelTimeout: time.Second,
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
