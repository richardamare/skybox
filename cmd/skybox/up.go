package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/richardamare/skybox/internal/sandbox"
)

func (d deps) upCommand(asJSON *bool) *cobra.Command {
	var size string
	cmd := &cobra.Command{
		Use:   "up <name>",
		Short: "Create a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: runE(asJSON, func(cmd *cobra.Command, args []string) (result, error) {
			return d.up(cmd, args[0], size)
		}),
	}
	cmd.Flags().StringVar(&size, "size", string(sandbox.Standard), "light, standard or large")
	return cmd
}

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

	progress(cmd, "creating %s (%s)", name, size)
	created, err := provider.Create(ctx, sandbox.Spec{Name: name, Size: size, SSHKeys: keys})
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", name, err)
	}
	return sandboxResult{created}, nil
}

type sandboxResult struct {
	sandbox.Sandbox
}

func (r sandboxResult) writeText(w io.Writer) {
	fmt.Fprintf(w, "%s is %s on %s (%s)\n", r.Name, r.State, r.MachineType, r.Size)
}
