package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/richardamare/skybox"
	"github.com/richardamare/skybox/internal/azure"
	"github.com/richardamare/skybox/internal/cloudflare"
	"github.com/richardamare/skybox/internal/control"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, connect))
}

const usage = `usage:
  skyboxctl reconcile [flags]   put idle sandboxes to sleep and delete stale resources, once
  skyboxctl --version
`

// connect reaches Azure with the environment's credential, a managed identity in the Container Apps Job and the
// Azure CLI on a Mac, and Cloudflare with the credentials in CLOUDFLARE_API_TOKEN, CLOUDFLARE_ACCOUNT_ID,
// CLOUDFLARE_ZONE_ID and CLOUDFLARE_ZONE.
func connect(ctx context.Context) (control.Cloud, control.Edge, error) {
	creds := cloudflare.Credentials{
		Token:     os.Getenv("CLOUDFLARE_API_TOKEN"),
		AccountID: os.Getenv("CLOUDFLARE_ACCOUNT_ID"),
		ZoneID:    os.Getenv("CLOUDFLARE_ZONE_ID"),
		Zone:      os.Getenv("CLOUDFLARE_ZONE"),
	}
	if creds.Token == "" || creds.AccountID == "" || creds.ZoneID == "" || creds.Zone == "" {
		return nil, nil, errors.New("set CLOUDFLARE_API_TOKEN, CLOUDFLARE_ACCOUNT_ID, CLOUDFLARE_ZONE_ID and CLOUDFLARE_ZONE")
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, nil, err
	}
	provider, err := azure.New(ctx, azure.Prod, cred)
	if err != nil {
		return nil, nil, err
	}
	return provider, cloudflare.New(creds), nil
}

type connector func(ctx context.Context) (control.Cloud, control.Edge, error)

func run(ctx context.Context, args []string, stdout, stderr io.Writer, connect connector) int {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-version") {
		fmt.Fprintf(stdout, "skyboxctl %s\n", skybox.Version)
		return 0
	}
	if len(args) == 0 || args[0] != "reconcile" {
		fmt.Fprint(stderr, usage)
		return 2
	}

	flags := flag.NewFlagSet("skyboxctl reconcile", flag.ContinueOnError)
	flags.SetOutput(stderr)
	enforce := flags.Bool("enforce", false, "act on the plan; without it the run only logs what it would do")
	policy := control.DefaultPolicy
	flags.DurationVar(&policy.IdleAfter, "idle-after", policy.IdleAfter, "how long a running sandbox may go without a running thread")
	flags.DurationVar(&policy.AsleepFor, "asleep-for", policy.AsleepFor, "how long a sandbox may sleep before it goes down")
	flags.DurationVar(&policy.Grace, "grace", policy.Grace, "how long a leftover must stand unchanged before it is deleted")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "skyboxctl reconcile takes no arguments, got %q\n", flags.Args())
		return 2
	}

	log := slog.New(slog.NewJSONHandler(stdout, nil)).With("version", skybox.Version)
	cloud, edge, err := connect(ctx)
	if err != nil {
		log.Error("cannot connect", "error", err)
		return 1
	}
	r := control.Reconciler{Cloud: cloud, Edge: edge, Policy: policy, Enforce: *enforce, Log: log, Now: time.Now}
	if err := r.Run(ctx); err != nil {
		return 1
	}
	return 0
}
