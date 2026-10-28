package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func (d deps) downCommand(asJSON *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "down <name>",
		Short: "Delete a sandbox and everything it holds",
		Args:  cobra.ExactArgs(1),
		RunE: runE(asJSON, func(cmd *cobra.Command, args []string) (result, error) {
			name := args[0]
			provider, err := d.providerFor(cmd.Context(), name)
			if err != nil {
				return nil, err
			}
			progress(cmd, "deleting %s", name)
			if err := provider.Delete(cmd.Context(), name); err != nil {
				return nil, err
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
