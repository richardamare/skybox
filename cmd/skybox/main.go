package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/spf13/cobra"

	"github.com/richardamare/skybox"
	"github.com/richardamare/skybox/internal/azure"
	"github.com/richardamare/skybox/internal/cloudflare"
	"github.com/richardamare/skybox/internal/onepassword"
	"github.com/richardamare/skybox/internal/sandbox"
	"github.com/richardamare/skybox/internal/sshkeys"
	"github.com/richardamare/skybox/internal/tailscale"
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
	tunnels  func(ctx context.Context) (tunnels, error)
	tailnet  func(ctx context.Context) (tailnet, error)
	// connect hands the SSH connection to cloudflared, which carries it through Cloudflare Access.
	connect func(hostname string) error
	// answers reports whether T3 Code responds at a sandbox's tailnet address.
	answers func(ctx context.Context, url string) bool

	sshConfig  string
	knownHosts string
	// executable is the skybox binary that the ssh alias runs as its ProxyCommand.
	executable string
	// tunnelTimeout bounds how long up and wake wait for the sandbox's tunnel to connect.
	tunnelTimeout time.Duration
	// joinTimeout bounds each of up's waits once the tunnel is connected: for the sandbox's device on the tailnet, then
	// for T3 Code to answer at its tailnet address.
	joinTimeout  time.Duration
	pollInterval time.Duration
}

// tunnels is the part of Cloudflare a sandbox needs: its tunnel, the DNS records in front of it, and their health.
type tunnels interface {
	Hostname(sandbox string) string
	CreateTunnel(ctx context.Context, name string) (cloudflare.Tunnel, error)
	Route(ctx context.Context, tunnelID, hostname string) error
	CreateDNSRecords(ctx context.Context, sandbox, hostname, tunnelID string) error
	TunnelHealthy(ctx context.Context, tunnelID string) (bool, error)
	DeleteDNSRecords(ctx context.Context, sandbox string) error
	DeleteTunnel(ctx context.Context, tunnelID string) error
}

func defaultDeps() deps {
	home, _ := os.UserHomeDir()
	executable, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
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
		tunnels: func(ctx context.Context) (tunnels, error) {
			creds, err := onepassword.Cloudflare(ctx)
			if err != nil {
				return nil, err
			}
			return cloudflare.New(creds), nil
		},
		tailnet: func(ctx context.Context) (tailnet, error) {
			creds, err := onepassword.Tailscale(ctx)
			if err != nil {
				return nil, err
			}
			return tailscale.New(creds), nil
		},
		connect:       execCloudflared,
		answers:       answersHTTP,
		sshConfig:     filepath.Join(home, ".ssh", "config"),
		knownHosts:    filepath.Join(home, ".ssh", "skybox_known_hosts"),
		executable:    executable,
		tunnelTimeout: 10 * time.Minute,
		joinTimeout:   2 * time.Minute,
		pollInterval:  5 * time.Second,
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
		d.proxyCommand(),
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

// errTimedOut is what poll returns when done never reported true before the timeout.
var errTimedOut = errors.New("timed out")

// poll calls done every interval until it reports true or fails, or until timeout has passed.
func poll(ctx context.Context, timeout, interval time.Duration, done func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		ok, err := done()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return errTimedOut
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
