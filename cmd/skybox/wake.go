package main

import "github.com/spf13/cobra"

func (d deps) wakeCommand(asJSON *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "wake <name>",
		Short: "Start a sleeping sandbox with the disk it had, and sync its agent toolkit",
		Args:  cobra.ExactArgs(1),
		RunE: runE(asJSON, func(cmd *cobra.Command, args []string) (result, error) {
			return d.wake(cmd, args[0])
		}),
	}
}

// wake returns once the sandbox's tunnel connects and its agent toolkit matches the Mac's, so an agent that starts
// there next works as it would on the Mac.
func (d deps) wake(cmd *cobra.Command, name string) (result, error) {
	ctx := cmd.Context()
	provider, err := d.providerFor(ctx, name)
	if err != nil {
		return nil, err
	}
	asleep, err := provider.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	cf, err := d.tunnels(ctx)
	if err != nil {
		return nil, err
	}
	progress(cmd, "waking %s", name)
	if err := provider.Wake(ctx, name); err != nil {
		return nil, err
	}
	if id := asleep.Labels[labelTunnelID]; id != "" {
		if err := d.waitForTunnel(ctx, cmd, cf, name, id); err != nil {
			return nil, err
		}
	}
	if err := d.syncStarted(cmd, name); err != nil {
		return nil, err
	}
	woken, err := provider.Get(ctx, name)
	return newSandboxResult(woken), err
}
