package main

import "github.com/spf13/cobra"

func (d deps) wakeCommand(asJSON *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "wake <name>",
		Short: "Start a sleeping sandbox with the disk it had",
		Args:  cobra.ExactArgs(1),
		RunE: runE(asJSON, func(cmd *cobra.Command, args []string) (result, error) {
			name := args[0]
			provider, err := d.providerFor(cmd.Context(), name)
			if err != nil {
				return nil, err
			}
			progress(cmd, "waking %s", name)
			if err := provider.Wake(cmd.Context(), name); err != nil {
				return nil, err
			}
			woken, err := provider.Get(cmd.Context(), name)
			return sandboxResult{woken}, err
		}),
	}
}
