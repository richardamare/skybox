package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/spf13/cobra"

	"github.com/richardamare/skybox"
	"github.com/richardamare/skybox/internal/azure"
	"github.com/richardamare/skybox/internal/sandbox"
	"github.com/richardamare/skybox/internal/sshkeys"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(defaultDeps().execute(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// deps are the outside systems the commands reach. Tests replace them with fakes.
type deps struct {
	provider func(ctx context.Context) (sandbox.Provider, error)
	sshKeys  func(ctx context.Context) ([]string, error)
}

func defaultDeps() deps {
	return deps{
		provider: func(ctx context.Context) (sandbox.Provider, error) {
			cred, err := azidentity.NewAzureCLICredential(nil)
			if err != nil {
				return nil, err
			}
			return azure.New(ctx, azure.Prod, cred)
		},
		sshKeys: func(ctx context.Context) ([]string, error) {
			return sshkeys.FromAgent(ctx, "skybox")
		},
	}
}

// commandError is an error from a command that ran, which exits 1. Any other error is a wrong invocation and exits 2.
type commandError struct{ err error }

func (e *commandError) Error() string { return e.err.Error() }
func (e *commandError) Unwrap() error { return e.err }

// execute runs one command and returns the process's exit code.
func (d deps) execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var asJSON bool
	root := d.rootCommand(&asJSON)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	cmd, err := root.ExecuteContextC(ctx)
	if err == nil {
		return 0
	}
	if asJSON {
		writeJSON(stdout, map[string]string{"error": err.Error()})
	} else {
		fmt.Fprintf(stderr, "skybox: %v\n", err)
	}
	var failed *commandError
	if errors.As(err, &failed) {
		return 1
	}
	if !asJSON {
		fmt.Fprint(stderr, cmd.UsageString())
	}
	return 2
}

func (d deps) rootCommand(asJSON *bool) *cobra.Command {
	root := &cobra.Command{
		Use:           "skybox",
		Short:         "Create, sleep, wake and delete sandboxes where coding agents work",
		Version:       skybox.Version,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return errors.New("name a command")
		},
	}
	root.SetVersionTemplate("skybox {{.Version}}\n")
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().BoolVar(asJSON, "json", false, "print one JSON document on stdout")

	root.AddCommand(
		d.upCommand(asJSON),
		d.lsCommand(asJSON),
		d.sleepCommand(asJSON),
		d.wakeCommand(asJSON),
		d.downCommand(asJSON),
	)
	return root
}

// providerFor checks the name a command was given before it reaches the cloud.
func (d deps) providerFor(ctx context.Context, name string) (sandbox.Provider, error) {
	if err := sandbox.ValidateName(name); err != nil {
		return nil, err
	}
	return d.provider(ctx)
}

// runE adapts a command's body: it reports the result as text or JSON, and marks its errors as exit 1.
func runE(asJSON *bool, body func(cmd *cobra.Command, args []string) (result, error)) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		res, err := body(cmd, args)
		if err != nil {
			return &commandError{err}
		}
		if *asJSON {
			writeJSON(cmd.OutOrStdout(), res)
		} else {
			res.writeText(cmd.OutOrStdout())
		}
		return nil
	}
}

type result interface {
	writeText(w io.Writer)
}

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func progress(cmd *cobra.Command, format string, args ...any) {
	fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...)
}
