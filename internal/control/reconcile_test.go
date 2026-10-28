package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/richardamare/skybox/internal/azure"
	"github.com/richardamare/skybox/internal/cloudflare"
	"github.com/richardamare/skybox/internal/sandbox"
)

// world is Azure and Cloudflare in memory. Its actions change what the next run reads, and it records each one.
type world struct {
	machines    map[string]azure.Machine
	activity    map[string]azure.Activity
	activityErr map[string]error
	leftovers   []azure.Leftover
	tunnels     []cloudflare.NamedTunnel
	records     []cloudflare.DNSRecord
	listErr     error
	changes     []string
}

func (w *world) Machines(context.Context) ([]azure.Machine, error) {
	if w.listErr != nil {
		return nil, w.listErr
	}
	var all []azure.Machine
	for _, m := range w.machines {
		all = append(all, m)
	}
	return all, nil
}

func (w *world) Activity(_ context.Context, name string) (azure.Activity, error) {
	if err := w.activityErr[name]; err != nil {
		return azure.Activity{}, err
	}
	return w.activity[name], nil
}

func (w *world) Leftovers(context.Context) ([]azure.Leftover, error) { return w.leftovers, nil }

func (w *world) Sleep(_ context.Context, name string) error {
	m := w.machines[name]
	m.State, m.AsleepSince = sandbox.Asleep, now
	w.machines[name] = m
	w.changes = append(w.changes, "sleep "+name)
	return nil
}

func (w *world) Delete(_ context.Context, name string) error {
	delete(w.machines, name)
	w.changes = append(w.changes, "delete "+name)
	return nil
}

func (w *world) DeleteLeftover(_ context.Context, l azure.Leftover) error {
	w.leftovers = slices.DeleteFunc(w.leftovers, func(x azure.Leftover) bool { return x.ID == l.ID })
	w.changes = append(w.changes, "delete "+l.Name)
	return nil
}

func (w *world) ReleaseOutboundIfIdle(context.Context) error {
	w.leftovers = slices.DeleteFunc(w.leftovers, azure.Leftover.Outbound)
	w.changes = append(w.changes, "release outbound")
	return nil
}

func (w *world) ListTunnels(context.Context) ([]cloudflare.NamedTunnel, error) { return w.tunnels, nil }

func (w *world) ListCNAMERecords(context.Context) ([]cloudflare.DNSRecord, error) {
	return w.records, nil
}

func (w *world) DeleteTunnel(_ context.Context, id string) error {
	w.tunnels = slices.DeleteFunc(w.tunnels, func(t cloudflare.NamedTunnel) bool { return t.ID == id })
	w.changes = append(w.changes, "delete tunnel "+id)
	return nil
}

func (w *world) DeleteDNSRecords(_ context.Context, name string) error {
	w.records = slices.DeleteFunc(w.records, func(r cloudflare.DNSRecord) bool { return r.Comment == "skybox:sandbox="+name })
	w.changes = append(w.changes, "delete records of "+name)
	return nil
}

// messyWorld has one of everything the control plane acts on.
func messyWorld() *world {
	sandboxTags := func(name string) map[string]string {
		return map[string]string{"skybox:component": "sandbox", "skybox:sandbox": name}
	}
	return &world{
		machines: map[string]azure.Machine{
			"idle":  machine("idle", sandbox.Running),
			"busy":  machine("busy", sandbox.Running),
			"stale": asleep("stale", 15*24*time.Hour),
		},
		activity: map[string]azure.Activity{
			"idle": idle(3*time.Hour, 2*time.Hour),
			"busy": {Booted: ago(3 * time.Hour), LatestRun: ago(3 * time.Hour), Running: 1},
		},
		leftovers: []azure.Leftover{
			{Kind: azure.KindDisk, ID: "d", Name: "osdisk-skybox-gone", Tags: sandboxTags("gone"), Created: ago(2 * time.Hour)},
		},
		tunnels: []cloudflare.NamedTunnel{
			{ID: "t-stale", Name: "skybox-stale", CreatedAt: ago(30 * 24 * time.Hour)},
			{ID: "t-gone", Name: "skybox-gone", CreatedAt: ago(2 * time.Hour)},
		},
		records: []cloudflare.DNSRecord{
			{ID: "1", Name: "stale-sbx.example.com", Comment: "skybox:sandbox=stale", CreatedOn: ago(30 * 24 * time.Hour)},
			{ID: "2", Name: "gone-sbx.example.com", Comment: "skybox:sandbox=gone", CreatedOn: ago(2 * time.Hour)},
		},
	}
}

func reconciler(w *world, enforce bool, logs *bytes.Buffer) Reconciler {
	return Reconciler{Cloud: w, Edge: w, Policy: DefaultPolicy, Enforce: enforce,
		Log: slog.New(slog.NewJSONHandler(logs, nil)), Now: func() time.Time { return now }}
}

func logLines(t *testing.T, logs *bytes.Buffer, msg string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for line := range strings.Lines(logs.String()) {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line %q is not JSON: %v", line, err)
		}
		if entry["msg"] == msg {
			lines = append(lines, entry)
		}
	}
	return lines
}

func TestDryRunLogsEveryActionAndChangesNothing(t *testing.T) {
	w := messyWorld()
	var logs bytes.Buffer

	err := reconciler(w, false, &logs).Run(context.Background())

	if err != nil || len(w.changes) != 0 {
		t.Fatalf("dry run = %v and changed %q; want no error and no change", err, w.changes)
	}
	var planned []string
	for _, line := range logLines(t, &logs, "would act") {
		if line["mode"] != "dry-run" || line["reason"] == "" {
			t.Errorf("log line %v lacks the dry-run mode or a reason", line)
		}
		planned = append(planned, line["action"].(string)+" "+line["target"].(string))
	}
	want := []string{"sleep idle", "down stale", "delete-leftover disk osdisk-skybox-gone", "delete-tunnel skybox-gone", "delete-dns-records gone"}
	if !slices.Equal(planned, want) {
		t.Fatalf("dry run logged %q, want %q", planned, want)
	}
}

func TestEnforcingActsAndASecondRunTakesNoAction(t *testing.T) {
	w := messyWorld()
	var logs bytes.Buffer

	if err := reconciler(w, true, &logs).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"sleep idle", "delete stale", "delete records of stale", "delete tunnel t-stale",
		"delete osdisk-skybox-gone", "delete tunnel t-gone", "delete records of gone"}
	if !slices.Equal(w.changes, want) {
		t.Fatalf("first run changed %q, want %q", w.changes, want)
	}
	if _, ok := w.machines["busy"]; !ok || w.machines["busy"].State != sandbox.Running {
		t.Fatal("the sandbox with a running thread did not stay awake")
	}

	w.changes = nil
	logs.Reset()
	if err := reconciler(w, true, &logs).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(w.changes) != 0 || len(logLines(t, &logs, "acted")) != 0 {
		t.Fatalf("second run changed %q; want no action", w.changes)
	}
}

func TestARunThatCannotListSandboxesChangesNothing(t *testing.T) {
	w := messyWorld()
	w.listErr = errors.New("throttled")
	var logs bytes.Buffer

	err := reconciler(w, true, &logs).Run(context.Background())

	if err == nil || len(w.changes) != 0 {
		t.Fatalf("run = %v and changed %q; want an error and no change", err, w.changes)
	}
}

func TestASandboxThatDoesNotReportStaysAndTheRunFails(t *testing.T) {
	w := messyWorld()
	w.activityErr = map[string]error{"idle": errors.New("run command timed out")}
	var logs bytes.Buffer

	err := reconciler(w, true, &logs).Run(context.Background())

	if err == nil || !strings.Contains(err.Error(), "idle") {
		t.Fatalf("run = %v; want an error naming idle", err)
	}
	if w.machines["idle"].State != sandbox.Running {
		t.Fatal("the sandbox that did not report went to sleep")
	}
	if !slices.Contains(w.changes, "delete stale") {
		t.Fatalf("changes %q; want the rest of the plan carried out", w.changes)
	}
}
