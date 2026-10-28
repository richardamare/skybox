package main

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/richardamare/skybox/internal/sandbox"
)

func (d deps) lsCommand(asJSON *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List sandboxes with their size and state",
		Args:  cobra.NoArgs,
		RunE: runE(asJSON, func(cmd *cobra.Command, _ []string) (result, error) {
			provider, err := d.provider(cmd.Context())
			if err != nil {
				return nil, err
			}
			sandboxes, err := provider.List(cmd.Context())
			if err != nil {
				return nil, err
			}
			slices.SortFunc(sandboxes, func(a, b sandbox.Sandbox) int { return strings.Compare(a.Name, b.Name) })
			return listResult{Sandboxes: append([]sandbox.Sandbox{}, sandboxes...)}, nil
		}),
	}
}

type listResult struct {
	Sandboxes []sandbox.Sandbox `json:"sandboxes"`
}

func (r listResult) writeText(w io.Writer) {
	if len(r.Sandboxes) == 0 {
		fmt.Fprintln(w, "no sandboxes")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSIZE\tMACHINE\tSTATE")
	for _, s := range r.Sandboxes {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Name, s.Size, s.MachineType, s.State)
	}
	_ = tw.Flush()
}
