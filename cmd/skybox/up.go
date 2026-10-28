package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/richardamare/skybox/internal/sandbox"
	"github.com/richardamare/skybox/internal/sshconfig"
)

func (d deps) upCommand(asJSON *bool) *cobra.Command {
	var size string
	cmd := &cobra.Command{
		Use:   "up <name>",
		Short: "Create a sandbox and the ssh alias that reaches it",
		Args:  cobra.ExactArgs(1),
		RunE: runE(asJSON, func(cmd *cobra.Command, args []string) (result, error) {
			return d.up(cmd, args[0], size)
		}),
	}
	cmd.Flags().StringVar(&size, "size", string(sandbox.Standard), "light, standard or large")
	return cmd
}

// up checks everything it can before it creates anything, and removes the tunnel and DNS record again when the
// sandbox itself cannot be created.
func (d deps) up(cmd *cobra.Command, name, sizeName string) (result, error) {
	ctx := cmd.Context()
	if err := sandbox.ValidateName(name); err != nil {
		return nil, err
	}
	size, err := sandbox.ParseSize(sizeName)
	if err != nil {
		return nil, err
	}
	provider, err := d.provider(ctx)
	if err != nil {
		return nil, err
	}
	existing, err := provider.Get(ctx, name)
	if err == nil {
		return nil, fmt.Errorf("sandbox %s already exists (%s, %s)", existing.Name, existing.Size, existing.State)
	}
	var notFound *sandbox.NotFoundError
	if !errors.As(err, &notFound) {
		return nil, err
	}
	if err := provider.CheckCapacity(ctx, size); err != nil {
		return nil, err
	}
	keys, err := d.sshKeys(ctx)
	if err != nil {
		return nil, err
	}
	cf, err := d.tunnels(ctx)
	if err != nil {
		return nil, err
	}

	hostname := cf.Hostname(name)
	progress(cmd, "creating the tunnel for %s", hostname)
	tunnel, err := cf.CreateTunnel(ctx, sshconfig.Alias(name))
	if err != nil {
		return nil, err
	}
	labels := map[string]string{labelTunnelID: tunnel.ID, labelHostname: hostname}
	release := func(cause error) error {
		cleanup := context.WithoutCancel(ctx)
		if id := labels[labelDNSRecord]; id != "" {
			cause = errors.Join(cause, cf.DeleteDNSRecord(cleanup, id))
		}
		return errors.Join(cause, cf.DeleteTunnel(cleanup, tunnel.ID))
	}
	if err := cf.RouteSSH(ctx, tunnel.ID, hostname); err != nil {
		return nil, release(err)
	}
	recordID, err := cf.CreateDNSRecord(ctx, name, hostname, tunnel.ID)
	if err != nil {
		return nil, release(err)
	}
	labels[labelDNSRecord] = recordID

	progress(cmd, "creating %s (%s)", name, size)
	created, err := provider.Create(ctx, sandbox.Spec{Name: name, Size: size, SSHKeys: keys, TunnelToken: tunnel.Token, Labels: labels})
	if err != nil {
		return nil, release(fmt.Errorf("create %s: %w", name, err))
	}
	block := sshconfig.Block(name, hostname, d.executable, d.knownHosts)
	catchAll := sshconfig.CatchAll(d.executable, d.knownHosts)
	if err := sshconfig.Update(d.sshConfig, func(c string) string {
		return sshconfig.WithCatchAll(sshconfig.With(c, name, block), catchAll)
	}); err != nil {
		return nil, fmt.Errorf("write the ssh alias %s to %s: %w", sshconfig.Alias(name), d.sshConfig, err)
	}
	if err := d.waitForTunnel(ctx, cmd, cf, name, tunnel.ID); err != nil {
		return nil, err
	}
	return newSandboxResult(created), nil
}
