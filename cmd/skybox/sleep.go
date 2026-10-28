package main

import "github.com/spf13/cobra"

func (d deps) sleepCommand(asJSON *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "sleep <name>",
		Short: "Stop a sandbox's compute and keep its disk",
		Args:  cobra.ExactArgs(1),
		RunE: runE(asJSON, func(cmd *cobra.Command, args []string) (result, error) {
			name := args[0]
			provider, err := d.providerFor(cmd.Context(), name)
			if err != nil {
				return nil, err
			}
			progress(cmd, "putting %s to sleep", name)
			if err := provider.Sleep(cmd.Context(), name); err != nil {
				return nil, err
			}
			slept, err := provider.Get(cmd.Context(), name)
			return sandboxResult{slept}, err
		}),
	}
}
