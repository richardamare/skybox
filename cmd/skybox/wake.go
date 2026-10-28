package main

import "github.com/spf13/cobra"

func (d deps) wakeCommand(asJSON *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "wake <name>",
		Short: "Start a sleeping sandbox with the disk it had",
		Args:  cobra.ExactArgs(1),
		RunE: runE(asJSON, func(cmd *cobra.Command, args []string) (result, error) {
			ctx, name := cmd.Context(), args[0]
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
			woken, err := provider.Get(ctx, name)
			return newSandboxResult(woken), err
		}),
	}
}
