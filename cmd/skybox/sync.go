package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/richardamare/skybox/internal/sshconfig"
	"github.com/richardamare/skybox/internal/toolkit"
)

func (d deps) syncCommand(asJSON *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "sync <name>",
		Short: "Give a running sandbox the agent toolkit: its three plugins and CLAUDE.md from the Mac's agent-config",
		Args:  cobra.ExactArgs(1),
		RunE: runE(asJSON, func(cmd *cobra.Command, args []string) (result, error) {
			ctx, name := cmd.Context(), args[0]
			if _, err := d.running(ctx, name); err != nil {
				return nil, err
			}
			if err := toolkit.CheckClone(d.agentConfig); err != nil {
				return nil, err
			}
			return d.syncToolkit(cmd, name)
		}),
	}
}

// syncStarted is the last step of up and wake. Without an agent-config clone on the Mac it skips the toolkit and says
// so, and any other failure leaves the sandbox running for skybox sync.
func (d deps) syncStarted(cmd *cobra.Command, name string) error {
	err := toolkit.CheckClone(d.agentConfig)
	var missing *toolkit.MissingCloneError
	if errors.As(err, &missing) {
		progress(cmd, "skipped the agent toolkit on %s: %v, so run skybox sync %s once it is there", name, err, name)
		return nil
	}
	if err == nil {
		_, err = d.syncToolkit(cmd, name)
	}
	if err != nil {
		return fmt.Errorf("%s is running, but its agent toolkit did not sync, so run skybox sync %s to try again: %w", name, name, err)
	}
	return nil
}

// syncToolkit copies the Mac's agent-config clone to a running sandbox and runs the toolkit's script there, over the
// sandbox's ssh alias. The whole archive is read before ssh starts, so a clone that cannot be read changes nothing.
func (d deps) syncToolkit(cmd *cobra.Command, name string) (syncResult, error) {
	progress(cmd, "syncing the agent toolkit to %s from %s", name, d.agentConfig)
	var archive, list bytes.Buffer
	if err := toolkit.Archive(&archive, d.agentConfig); err != nil {
		return syncResult{}, fmt.Errorf("read the agent-config clone at %s: %w", d.agentConfig, err)
	}
	command := "bash -c " + toolkit.Quote(toolkit.Script())
	if err := d.ssh(cmd.Context(), sshconfig.Alias(name), command, &archive, &list, cmd.ErrOrStderr()); err != nil {
		return syncResult{}, fmt.Errorf("sync the agent toolkit to %s: %w", name, err)
	}
	plugins, err := toolkit.CheckInstalled(list.Bytes())
	if err != nil {
		return syncResult{}, fmt.Errorf("sync the agent toolkit to %s: %w", name, err)
	}
	return syncResult{Name: name, Plugins: plugins, Instructions: "~/.claude/CLAUDE.md"}, nil
}

type syncResult struct {
	Name    string              `json:"name"`
	Plugins []toolkit.Installed `json:"plugins"`
	// Instructions is where the sandbox holds agent-config's instructions/AGENTS.md.
	Instructions string `json:"instructions"`
}

func (r syncResult) writeText(w io.Writer) {
	fmt.Fprintf(w, "%s has the agent toolkit:\n", r.Name)
	for _, p := range r.Plugins {
		fmt.Fprintf(w, "  %s %s\n", p.ID, p.Version)
	}
	fmt.Fprintf(w, "  %s from agent-config's %s\n", r.Instructions, toolkit.Instructions)
}

// execSSH runs command on the sandbox behind alias with the user's ssh, which reaches it through skybox proxy.
func execSSH(ctx context.Context, alias, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	ssh := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", alias, command)
	ssh.Stdin, ssh.Stdout, ssh.Stderr = stdin, stdout, stderr
	return ssh.Run()
}
