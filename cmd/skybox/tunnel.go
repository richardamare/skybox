package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/richardamare/skybox/internal/cloudflare"
	"github.com/richardamare/skybox/internal/sandbox"
	"github.com/richardamare/skybox/internal/sshconfig"
)

// The labels skybox stores with a sandbox, so down finds its tunnel, and proxy and previews its hostname.
const (
	labelTunnelID = "tunnel-id"
	labelHostname = "hostname"
)

type sandboxResult struct {
	sandbox.Sandbox
	SSHAlias string   `json:"sshAlias,omitempty"`
	Hostname string   `json:"hostname,omitempty"`
	Previews []string `json:"previews,omitempty"`
}

func newSandboxResult(s sandbox.Sandbox) sandboxResult {
	r := sandboxResult{Sandbox: s, Hostname: s.Labels[labelHostname]}
	if r.Hostname == "" {
		return r
	}
	r.SSHAlias = sshconfig.Alias(s.Name)
	for _, port := range cloudflare.PreviewPorts {
		r.Previews = append(r.Previews, "https://"+cloudflare.PreviewHostname(r.Hostname, port))
	}
	return r
}

func (r sandboxResult) writeText(w io.Writer) {
	fmt.Fprintf(w, "%s is %s on %s (%s)\n", r.Name, r.State, r.MachineType, r.Size)
	if r.SSHAlias == "" || r.State != sandbox.Running {
		return
	}
	ports := cloudflare.PreviewPorts
	fmt.Fprintf(w, "connect with: ssh %s\n", r.SSHAlias)
	fmt.Fprintf(w, "preview ports %d to %d at: https://<port>-%s\n", ports[0], ports[len(ports)-1], r.Hostname)
}

// waitForTunnel returns once cloudflared on the sandbox holds a connection, which cloud-init starts last.
func (d deps) waitForTunnel(ctx context.Context, cmd *cobra.Command, cf tunnels, name, tunnelID string) error {
	progress(cmd, "waiting for %s to connect its tunnel", name)
	deadline := time.Now().Add(d.tunnelTimeout)
	for {
		healthy, err := cf.TunnelHealthy(ctx, tunnelID)
		if err != nil {
			return err
		}
		if healthy {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not connect its tunnel within %s: it is left as it is, so read its boot log with `az vm boot-diagnostics get-boot-log`, or delete it with skybox down %s", name, d.tunnelTimeout, name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d.pollInterval):
		}
	}
}
