package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/richardamare/skybox/internal/sandbox"
)

// fakeProvider keeps sandboxes in memory and gives each size the machine type the Azure provider uses.
type fakeProvider struct {
	sandboxes map[string]sandbox.Sandbox
	capacity  error
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

func testDeps(p *fakeProvider) deps {
	return deps{
		provider: func(context.Context) (sandbox.Provider, error) { return p, nil },
		sshKeys: func(context.Context) ([]string, error) {
			return []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFake test"}, nil
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
