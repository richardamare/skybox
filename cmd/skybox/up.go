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

// up checks everything it can before it creates anything. A sandbox needs its own auth key, its tunnel and its DNS
// records before its VM, so when a later step fails, up revokes and deletes what it made for the sandbox so far.
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
	ts, err := d.tailnet(ctx)
	if err != nil {
		return nil, err
	}

	// No sandbox has this name, so a device of this name is a leftover, and the new one would get a suffixed address.
	if err := ts.DeleteDevices(ctx, name); err != nil {
		return nil, err
	}
	progress(cmd, "minting the tailnet auth key for %s", name)
	authKey, err := ts.MintAuthKey(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("mint the tailnet auth key for %s: %w", name, err)
	}
	revoke := func(cause error) error {
		return errors.Join(cause, ts.RevokeAuthKey(context.WithoutCancel(ctx), authKey.ID))
	}

	hostname := cf.Hostname(name)
	progress(cmd, "creating the tunnel for %s", hostname)
	tunnel, err := cf.CreateTunnel(ctx, sshconfig.Alias(name))
	if err != nil {
		return nil, revoke(err)
	}
	release := func(cause error) error {
		cleanup := context.WithoutCancel(ctx)
		return revoke(errors.Join(cause, cf.DeleteDNSRecords(cleanup, name), cf.DeleteTunnel(cleanup, tunnel.ID)))
	}
	if err := cf.Route(ctx, tunnel.ID, hostname); err != nil {
		return nil, release(err)
	}
	if err := cf.CreateDNSRecords(ctx, name, hostname, tunnel.ID); err != nil {
		return nil, release(err)
	}
	labels := map[string]string{labelTunnelID: tunnel.ID, labelHostname: hostname}

	progress(cmd, "creating %s (%s)", name, size)
	created, err := provider.Create(ctx, sandbox.Spec{
		Name: name, Size: size, SSHKeys: keys, TunnelToken: tunnel.Token, TailnetAuthKey: authKey.Key, Labels: labels,
	})
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
	device, err := d.waitForTailnet(ctx, cmd, ts, name)
	if err != nil {
		return nil, err
	}
	res := newSandboxResult(created)
	res.TailnetURL = "https://" + device.Name
	return res, nil
}
