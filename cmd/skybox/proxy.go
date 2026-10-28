package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/richardamare/skybox/internal/sandbox"
	"github.com/richardamare/skybox/internal/sshconfig"
)

// proxyCommand is the ProxyCommand of every skybox ssh alias. It connects only to a running sandbox and fails at once
// otherwise, because T3 Code reconnects saved environments by itself and gives up on SSH within seconds.
func (d deps) proxyCommand() *cobra.Command {
	var alias bool
	cmd := &cobra.Command{
		Use:    "proxy <name>",
		Short:  "Carry an ssh connection to a running sandbox; ssh runs it from the alias",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// ssh started from the T3 Code desktop inherits a PATH without Homebrew, where az and cloudflared live.
			os.Setenv("PATH", withToolPath(os.Getenv("PATH")))
			ctx, name := cmd.Context(), args[0]
			if alias {
				name = strings.TrimPrefix(name, sshconfig.Alias(""))
			}
			provider, err := d.providerFor(ctx, name)
			if err != nil {
				return &commandError{err}
			}
			s, err := provider.Get(ctx, name)
			var notFound *sandbox.NotFoundError
			switch {
			case errors.As(err, &notFound):
				return &commandError{fmt.Errorf("no sandbox named %s: create it with skybox up %s", name, name)}
			case err != nil:
				return &commandError{err}
			case s.State == sandbox.Asleep:
				return &commandError{fmt.Errorf("%s is asleep, run skybox wake %s", name, name)}
			case s.State != sandbox.Running:
				return &commandError{fmt.Errorf("%s is %s, and accepts ssh once it is running", name, s.State)}
			case s.Labels[labelHostname] == "":
				return &commandError{fmt.Errorf("%s has no tunnel hostname", name)}
			}
			return d.connect(s.Labels[labelHostname])
		},
	}
	cmd.Flags().BoolVar(&alias, "alias", false, "the argument is a skybox-<name> alias, as the catch-all ssh block passes it")
	return cmd
}

func withToolPath(path string) string {
	dirs := strings.Split(path, ":")
	for _, dir := range []string{"/usr/local/bin", "/opt/homebrew/bin"} {
		if !slices.Contains(dirs, dir) {
			dirs = append([]string{dir}, dirs...)
		}
	}
	return strings.Join(dirs, ":")
}

// execCloudflared replaces skybox with cloudflared, which then owns ssh's stdin and stdout.
func execCloudflared(hostname string) error {
	path, err := exec.LookPath("cloudflared")
	if err != nil {
		return &commandError{errors.New("cloudflared is not installed: brew install cloudflared")}
	}
	return &commandError{syscall.Exec(path, []string{"cloudflared", "access", "ssh", "--hostname", hostname}, os.Environ())}
}
