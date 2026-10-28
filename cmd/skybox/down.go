package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/richardamare/skybox/internal/sshconfig"
)

func (d deps) downCommand(asJSON *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "down <name>",
		Short: "Delete a sandbox, its tunnel, its DNS record and its ssh alias",
		Args:  cobra.ExactArgs(1),
		RunE: runE(asJSON, func(cmd *cobra.Command, args []string) (result, error) {
			ctx, name := cmd.Context(), args[0]
			provider, err := d.providerFor(ctx, name)
			if err != nil {
				return nil, err
			}
			doomed, err := provider.Get(ctx, name)
			if err != nil {
				return nil, err
			}
			cf, err := d.tunnels(ctx)
			if err != nil {
				return nil, err
			}
			progress(cmd, "deleting %s", name)
			if err := provider.Delete(ctx, name); err != nil {
				return nil, err
			}
			// The VM is gone, so cloudflared holds no connection and Cloudflare lets the tunnel go.
			var cleanup error
			if id := doomed.Labels[labelDNSRecord]; id != "" {
				cleanup = errors.Join(cleanup, cf.DeleteDNSRecord(ctx, id))
			}
			if id := doomed.Labels[labelTunnelID]; id != "" {
				cleanup = errors.Join(cleanup, cf.DeleteTunnel(ctx, id))
			}
			cleanup = errors.Join(cleanup,
				sshconfig.Update(d.sshConfig, func(c string) string { return sshconfig.Without(c, name) }),
				sshconfig.ForgetHost(d.knownHosts, sshconfig.Alias(name)))
			if cleanup != nil {
				return nil, fmt.Errorf("deleted %s, but not everything around it: %w", name, cleanup)
			}
			return deletedResult{Name: name, Deleted: true}, nil
		}),
	}
}

type deletedResult struct {
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

func (r deletedResult) writeText(w io.Writer) {
	fmt.Fprintf(w, "%s is deleted\n", r.Name)
}
