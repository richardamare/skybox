// Package sandbox holds the domain of skybox: sandbox names, size profiles, states, and the Provider interface
// every cloud implements.
package sandbox

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// Size is a profile name the user picks with --size. Each provider maps it to a machine type.
type Size string

const (
	Light    Size = "light"
	Standard Size = "standard"
	Large    Size = "large"
)

// Sizes lists every profile, smallest first.
var Sizes = []Size{Light, Standard, Large}

func ParseSize(s string) (Size, error) {
	for _, size := range Sizes {
		if string(size) == s {
			return size, nil
		}
	}
	return "", fmt.Errorf("unknown size %q: choose %s", s, sizeList())
}

func sizeList() string {
	names := make([]string, len(Sizes))
	for i, size := range Sizes {
		names[i] = string(size)
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// A name becomes part of a DNS label and of cloud resource names, so it is short, lowercase and starts with a letter.
var namePattern = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,28}[a-z0-9])?$`)

func ValidateName(name string) error {
	if !namePattern.MatchString(name) || strings.Contains(name, "--") {
		return fmt.Errorf("invalid sandbox name %q: use 1 to 30 lowercase letters, digits and single hyphens, starting with a letter and ending with a letter or digit", name)
	}
	return nil
}

type State string

const (
	Creating State = "creating"
	Running  State = "running"
	Sleeping State = "sleeping"
	Asleep   State = "asleep"
	Waking   State = "waking"
	Deleting State = "deleting"
	Failed   State = "failed"
)

type Sandbox struct {
	Name        string `json:"name"`
	Size        Size   `json:"size"`
	MachineType string `json:"machineType"`
	State       State  `json:"state"`
	// Labels are the values skybox stored with the sandbox when it created it, such as its tunnel's id.
	Labels map[string]string `json:"-"`
}

type Spec struct {
	Name    string
	Size    Size
	SSHKeys []string
	// TunnelToken lets cloudflared on the sandbox run its Cloudflare tunnel, which carries SSH and previews.
	TunnelToken string
	// TailnetAuthKey is the single-use key the sandbox joins the tailnet with on its first boot, so a link from T3
	// Code reaches its T3 Code server.
	TailnetAuthKey string
	Labels         map[string]string
}

// Provider creates, sleeps, wakes and deletes sandboxes in one cloud. The cloud is the only store of a sandbox's
// state, so every method reads it fresh.
type Provider interface {
	Get(ctx context.Context, name string) (Sandbox, error)
	List(ctx context.Context) ([]Sandbox, error)
	// CheckCapacity returns a *CapacityError when the cloud has too little quota left for one more sandbox of size.
	CheckCapacity(ctx context.Context, size Size) error
	Create(ctx context.Context, spec Spec) (Sandbox, error)
	Sleep(ctx context.Context, name string) error
	Wake(ctx context.Context, name string) error
	Delete(ctx context.Context, name string) error
}

type NotFoundError struct {
	Name string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("no sandbox named %s", e.Name)
}

type CapacityError struct {
	Size   Size
	Region string
	Family string
	Free   int
	Need   int
}

func (e *CapacityError) Error() string {
	return fmt.Sprintf("%s has %d vCPUs left in the %s family, and a %s sandbox needs %d", e.Region, e.Free, e.Family, e.Size, e.Need)
}
