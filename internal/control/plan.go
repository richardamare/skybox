// Package control is the control plane: it puts idle sandboxes to sleep, takes down sandboxes that slept too long,
// and deletes what a sandbox left behind in Azure and Cloudflare. Each run decides from the state it reads, so the
// next run repeats a crashed one.
package control

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/richardamare/skybox/internal/azure"
	"github.com/richardamare/skybox/internal/cloudflare"
	"github.com/richardamare/skybox/internal/sandbox"
	"github.com/richardamare/skybox/internal/sshconfig"
)

type Policy struct {
	// IdleAfter is how long a running sandbox may go without a T3 Code thread running before it sleeps.
	IdleAfter time.Duration
	// AsleepFor is how long a sandbox may sleep before it goes down.
	AsleepFor time.Duration
	// Grace is how old a leftover must be before it is deleted, so a sandbox that `skybox up` is still creating keeps
	// its parts.
	Grace time.Duration
}

var DefaultPolicy = Policy{IdleAfter: time.Hour, AsleepFor: 14 * 24 * time.Hour, Grace: time.Hour}

// Snapshot is everything one run reads before it decides.
type Snapshot struct {
	Now      time.Time
	Machines []azure.Machine
	// Activity holds a report for each running sandbox that answered.
	Activity  map[string]azure.Activity
	Leftovers []azure.Leftover
	Tunnels   []cloudflare.NamedTunnel
	Records   []cloudflare.DNSRecord
}

type Kind string

const (
	Sleep Kind = "sleep"
	// Down deletes the sandbox, then its DNS records and its tunnel.
	Down           Kind = "down"
	DeleteLeftover Kind = "delete-leftover"
	// ReleaseOutbound deletes the NAT gateway and its public IP, which no sandbox needs while none runs.
	ReleaseOutbound Kind = "release-outbound"
	DeleteTunnel    Kind = "delete-tunnel"
	// DeleteRecords deletes every DNS record whose comment names the sandbox.
	DeleteRecords Kind = "delete-dns-records"
)

// Action is one change. Target is the sandbox's name for Sleep, Down and DeleteRecords.
type Action struct {
	Kind   Kind
	Target string
	Reason string

	Leftovers []azure.Leftover
	Tunnels   []cloudflare.NamedTunnel
	Records   []cloudflare.DNSRecord
}

const labelTunnelID = "tunnel-id"

// Plan decides every action from the snapshot alone.
func Plan(s Snapshot, p Policy) []Action {
	var actions []Action
	machines := slices.SortedFunc(slices.Values(s.Machines), func(a, b azure.Machine) int { return cmp.Compare(a.Name, b.Name) })

	exists := map[string]bool{}
	liveTunnels := map[string]bool{}
	needsOutbound := false
	for _, m := range machines {
		exists[m.Name] = true
		if id := m.Labels[labelTunnelID]; id != "" {
			liveTunnels[id] = true
		}
		if m.State != sandbox.Asleep && m.State != sandbox.Failed {
			needsOutbound = true
		}
	}

	var tunnels []cloudflare.NamedTunnel
	for _, t := range s.Tunnels {
		if strings.HasPrefix(t.Name, sshconfig.Alias("")) {
			tunnels = append(tunnels, t)
		}
	}
	slices.SortFunc(tunnels, func(a, b cloudflare.NamedTunnel) int { return cmp.Compare(a.Name+a.ID, b.Name+b.ID) })
	records := map[string][]cloudflare.DNSRecord{}
	for _, r := range s.Records {
		if name, ok := r.Sandbox(); ok {
			records[name] = append(records[name], r)
		}
	}
	downTunnels := map[string]bool{}

	for _, m := range machines {
		if m.Keep {
			continue
		}
		switch m.State {
		case sandbox.Running:
			a, ok := s.Activity[m.Name]
			if !ok || a.Running > 0 {
				continue
			}
			if since := a.IdleSince(); s.Now.Sub(since) >= p.IdleAfter {
				actions = append(actions, Action{Kind: Sleep, Target: m.Name,
					Reason: fmt.Sprintf("no T3 Code thread has run since %s", since.UTC().Format(time.RFC3339))})
			}
		case sandbox.Asleep:
			if m.AsleepSince.IsZero() || s.Now.Sub(m.AsleepSince) < p.AsleepFor {
				continue
			}
			down := Action{Kind: Down, Target: m.Name, Records: records[m.Name],
				Reason: fmt.Sprintf("asleep since %s", m.AsleepSince.UTC().Format(time.RFC3339))}
			for _, t := range tunnels {
				if t.ID == m.Labels[labelTunnelID] || t.Name == sshconfig.Alias(m.Name) {
					down.Tunnels = append(down.Tunnels, t)
					downTunnels[t.ID] = true
				}
			}
			actions = append(actions, down)
		}
	}

	var outbound []azure.Leftover
	outboundSettled := true
	for _, l := range slices.SortedFunc(slices.Values(s.Leftovers), func(a, b azure.Leftover) int { return cmp.Compare(a.ID, b.ID) }) {
		settled := s.Now.Sub(l.Created) >= p.Grace
		if l.Outbound() {
			outbound = append(outbound, l)
			outboundSettled = outboundSettled && settled
			continue
		}
		if l.Sandbox() == "" || exists[l.Sandbox()] || l.Attached || l.HoldsVM || len(l.Untagged) > 0 || !settled {
			continue
		}
		actions = append(actions, Action{Kind: DeleteLeftover, Target: l.Kind + " " + l.Name,
			Reason: fmt.Sprintf("sandbox %s does not exist", l.Sandbox()), Leftovers: []azure.Leftover{l}})
	}
	// `skybox up` creates the tunnel before it gives sandboxes outbound access, so a young tunnel means a sandbox may
	// be on its way.
	for _, t := range tunnels {
		outboundSettled = outboundSettled && s.Now.Sub(t.CreatedAt) >= p.Grace
	}
	if len(outbound) > 0 && outboundSettled && !needsOutbound {
		actions = append(actions, Action{Kind: ReleaseOutbound, Target: "outbound access",
			Reason: "no sandbox runs", Leftovers: outbound})
	}

	for _, t := range tunnels {
		if downTunnels[t.ID] || liveTunnels[t.ID] || s.Now.Sub(t.CreatedAt) < p.Grace {
			continue
		}
		actions = append(actions, Action{Kind: DeleteTunnel, Target: t.Name,
			Reason: "no sandbox uses it", Tunnels: []cloudflare.NamedTunnel{t}})
	}
	for _, name := range slices.Sorted(maps.Keys(records)) {
		if exists[name] || !settledRecords(records[name], s.Now, p.Grace) {
			continue
		}
		actions = append(actions, Action{Kind: DeleteRecords, Target: name,
			Reason: fmt.Sprintf("sandbox %s does not exist", name), Records: records[name]})
	}
	return actions
}

// settledRecords reports whether every record is older than the grace period. `skybox up` creates a sandbox's
// records together, before its VM.
func settledRecords(records []cloudflare.DNSRecord, now time.Time, grace time.Duration) bool {
	for _, r := range records {
		if now.Sub(r.CreatedOn) < grace {
			return false
		}
	}
	return true
}
