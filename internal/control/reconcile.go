package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/richardamare/skybox/internal/azure"
	"github.com/richardamare/skybox/internal/cloudflare"
	"github.com/richardamare/skybox/internal/sandbox"
)

// Cloud is the part of the Azure provider the control plane reads and acts through.
type Cloud interface {
	Machines(ctx context.Context) ([]azure.Machine, error)
	Activity(ctx context.Context, name string) (azure.Activity, error)
	Leftovers(ctx context.Context) ([]azure.Leftover, error)
	Sleep(ctx context.Context, name string) error
	Delete(ctx context.Context, name string) error
	DeleteLeftover(ctx context.Context, l azure.Leftover) error
	ReleaseOutboundIfIdle(ctx context.Context) error
}

// Edge is the part of Cloudflare the control plane reads and acts through.
type Edge interface {
	ListTunnels(ctx context.Context) ([]cloudflare.NamedTunnel, error)
	ListCNAMERecords(ctx context.Context) ([]cloudflare.DNSRecord, error)
	DeleteTunnel(ctx context.Context, tunnelID string) error
	DeleteDNSRecords(ctx context.Context, sandbox string) error
}

type Reconciler struct {
	Cloud  Cloud
	Edge   Edge
	Policy Policy
	// Enforce makes the run act. Without it the run logs every action it would take and changes nothing.
	Enforce bool
	Log     *slog.Logger
	Now     func() time.Time
}

// Run reads the state, plans and, when enforcing, acts. A run that cannot read the whole state acts on none of it,
// since a missing sandbox would make its parts look left behind. It returns every error it met.
func (r Reconciler) Run(ctx context.Context) error {
	s, problems, err := r.observe(ctx)
	if err != nil {
		r.Log.Error("cannot read the state; nothing changes", "error", err)
		return err
	}
	actions := Plan(s, r.Policy)

	mode := "dry-run"
	if r.Enforce {
		mode = "enforce"
	}
	for _, a := range actions {
		log := r.Log.With("mode", mode, "action", string(a.Kind), "target", a.Target, "reason", a.Reason)
		if !r.Enforce {
			log.Info("would act")
			continue
		}
		if err := r.execute(ctx, a); err != nil {
			log.Error("action failed", "error", err)
			problems = append(problems, fmt.Errorf("%s %s: %w", a.Kind, a.Target, err))
			continue
		}
		log.Info("acted")
	}
	r.Log.Info("run finished", "mode", mode, "actions", len(actions), "errors", len(problems))
	return errors.Join(problems...)
}

// observe reads the whole state. A sandbox that does not report its activity is left out of the plan, and its error
// joins the problems.
func (r Reconciler) observe(ctx context.Context) (Snapshot, []error, error) {
	s := Snapshot{Now: r.Now(), Activity: map[string]azure.Activity{}}
	var err error
	if s.Machines, err = r.Cloud.Machines(ctx); err != nil {
		return s, nil, fmt.Errorf("list sandboxes: %w", err)
	}
	if s.Leftovers, err = r.Cloud.Leftovers(ctx); err != nil {
		return s, nil, fmt.Errorf("list Azure resources: %w", err)
	}
	if s.Tunnels, err = r.Edge.ListTunnels(ctx); err != nil {
		return s, nil, fmt.Errorf("list Cloudflare tunnels: %w", err)
	}
	if s.Records, err = r.Edge.ListCNAMERecords(ctx); err != nil {
		return s, nil, fmt.Errorf("list DNS records: %w", err)
	}

	var problems []error
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, m := range s.Machines {
		log := r.Log.With("sandbox", m.Name, "state", string(m.State), "keep", m.Keep)
		if m.Keep || m.State != sandbox.Running {
			if !m.AsleepSince.IsZero() {
				log = log.With("asleepSince", m.AsleepSince)
			}
			log.Info("sandbox")
			continue
		}
		wg.Go(func() {
			a, err := r.Cloud.Activity(ctx, m.Name)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				log.Error("sandbox did not report its activity; it stays as it is", "error", err)
				problems = append(problems, fmt.Errorf("read the activity of %s: %w", m.Name, err))
				return
			}
			s.Activity[m.Name] = a
			log.Info("sandbox", "runningThreads", a.Running, "idleSince", a.IdleSince())
		})
	}
	wg.Wait()
	return s, problems, nil
}

func (r Reconciler) execute(ctx context.Context, a Action) error {
	switch a.Kind {
	case Sleep:
		return r.Cloud.Sleep(ctx, a.Target)
	case Down:
		// The VM goes first, so cloudflared holds no connection and Cloudflare lets the tunnel go.
		if err := r.Cloud.Delete(ctx, a.Target); err != nil {
			return err
		}
		return errors.Join(r.Edge.DeleteDNSRecords(ctx, a.Target), r.deleteTunnels(ctx, a.Tunnels))
	case DeleteLeftover:
		return r.Cloud.DeleteLeftover(ctx, a.Leftovers[0])
	case ReleaseOutbound:
		if err := r.Cloud.ReleaseOutboundIfIdle(ctx); err != nil {
			return err
		}
		// Outbound resources under other names than the provider's own are deleted one by one.
		var errs []error
		for _, l := range a.Leftovers {
			errs = append(errs, r.Cloud.DeleteLeftover(ctx, l))
		}
		return errors.Join(errs...)
	case DeleteTunnel:
		return r.deleteTunnels(ctx, a.Tunnels)
	case DeleteRecords:
		return r.Edge.DeleteDNSRecords(ctx, a.Target)
	}
	return fmt.Errorf("unknown action %q", a.Kind)
}

func (r Reconciler) deleteTunnels(ctx context.Context, tunnels []cloudflare.NamedTunnel) error {
	var errs []error
	for _, t := range tunnels {
		errs = append(errs, r.Edge.DeleteTunnel(ctx, t.ID))
	}
	return errors.Join(errs...)
}
