package control

import (
	"testing"
	"time"

	"github.com/richardamare/skybox/internal/azure"
	"github.com/richardamare/skybox/internal/cloudflare"
	"github.com/richardamare/skybox/internal/sandbox"
)

var now = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return now.Add(-d) }

func machine(name string, state sandbox.State) azure.Machine {
	return azure.Machine{Sandbox: sandbox.Sandbox{Name: name, State: state, Labels: map[string]string{"tunnel-id": "t-" + name}}}
}

func asleep(name string, since time.Duration) azure.Machine {
	m := machine(name, sandbox.Asleep)
	m.AsleepSince = ago(since)
	return m
}

func idle(booted, latestRun time.Duration) azure.Activity {
	return azure.Activity{Booted: ago(booted), LatestRun: ago(latestRun)}
}

func kinds(actions []Action) []string {
	var out []string
	for _, a := range actions {
		out = append(out, string(a.Kind)+" "+a.Target)
	}
	return out
}

func assertActions(t *testing.T, actions []Action, want ...string) {
	t.Helper()
	got := kinds(actions)
	if len(got) != len(want) {
		t.Fatalf("actions = %q, want %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("actions = %q, want %q", got, want)
		}
	}
}

func TestARunningSandboxWithoutAThreadForAnHourSleeps(t *testing.T) {
	s := Snapshot{Now: now, Machines: []azure.Machine{machine("api-fix", sandbox.Running)},
		Activity: map[string]azure.Activity{"api-fix": idle(5*time.Hour, 61*time.Minute)}}

	actions := Plan(s, DefaultPolicy)

	assertActions(t, actions, "sleep api-fix")
}

func TestARunningSandboxWithARecentThreadStaysAwake(t *testing.T) {
	s := Snapshot{Now: now, Machines: []azure.Machine{machine("api-fix", sandbox.Running)},
		Activity: map[string]azure.Activity{"api-fix": idle(5*time.Hour, 59*time.Minute)}}

	assertActions(t, Plan(s, DefaultPolicy))
}

func TestASandboxWokenLessThanAnHourAgoStaysAwakeWhateverItsOldThreads(t *testing.T) {
	s := Snapshot{Now: now, Machines: []azure.Machine{machine("api-fix", sandbox.Running)},
		Activity: map[string]azure.Activity{"api-fix": idle(10*time.Minute, 72*time.Hour)}}

	assertActions(t, Plan(s, DefaultPolicy))
}

func TestASandboxWithAThreadStillRunningStaysAwakeHoweverOldItsLastRun(t *testing.T) {
	a := idle(5*time.Hour, 4*time.Hour)
	a.Running = 1
	s := Snapshot{Now: now, Machines: []azure.Machine{machine("api-fix", sandbox.Running)},
		Activity: map[string]azure.Activity{"api-fix": a}}

	assertActions(t, Plan(s, DefaultPolicy))
}

func TestARunningSandboxThatDidNotReportStaysAwake(t *testing.T) {
	s := Snapshot{Now: now, Machines: []azure.Machine{machine("api-fix", sandbox.Running)}, Activity: map[string]azure.Activity{}}

	assertActions(t, Plan(s, DefaultPolicy))
}

func TestASandboxAsleepForFourteenDaysGoesDownWithItsTunnelAndRecords(t *testing.T) {
	s := Snapshot{
		Now:      now,
		Machines: []azure.Machine{asleep("api-fix", 14*24*time.Hour)},
		Tunnels:  []cloudflare.NamedTunnel{{ID: "t-api-fix", Name: "skybox-api-fix", CreatedAt: ago(30 * 24 * time.Hour)}},
		Records: []cloudflare.DNSRecord{
			{ID: "r1", Name: "api-fix-sbx.example.com", Comment: "skybox:sandbox=api-fix"},
			{ID: "r2", Name: "3000-api-fix-sbx.example.com", Comment: "skybox:sandbox=api-fix"},
		},
	}

	actions := Plan(s, DefaultPolicy)

	assertActions(t, actions, "down api-fix")
	if len(actions[0].Tunnels) != 1 || len(actions[0].Records) != 2 {
		t.Fatalf("down takes %d tunnels and %d records, want 1 and 2", len(actions[0].Tunnels), len(actions[0].Records))
	}
}

func TestASandboxAsleepForLessThanFourteenDaysStays(t *testing.T) {
	s := Snapshot{Now: now, Machines: []azure.Machine{asleep("api-fix", 13*24*time.Hour)}}

	assertActions(t, Plan(s, DefaultPolicy))
}

func TestASandboxTaggedKeepStaysWhateverItsActivity(t *testing.T) {
	running := machine("running", sandbox.Running)
	running.Keep = true
	sleeping := asleep("sleeping", 90*24*time.Hour)
	sleeping.Keep = true
	s := Snapshot{Now: now, Machines: []azure.Machine{running, sleeping},
		Activity: map[string]azure.Activity{"running": idle(30*24*time.Hour, 30*24*time.Hour)}}

	assertActions(t, Plan(s, DefaultPolicy))
}

func TestOrphanedAzureResourcesPastTheirGraceAreDeleted(t *testing.T) {
	tags := func(name string) map[string]string {
		return map[string]string{"skybox:component": "sandbox", "skybox:sandbox": name}
	}
	s := Snapshot{
		Now:      now,
		Machines: []azure.Machine{machine("alive", sandbox.Running)},
		Activity: map[string]azure.Activity{"alive": idle(time.Minute, time.Minute)},
		Leftovers: []azure.Leftover{
			{Kind: azure.KindDisk, ID: "1", Name: "osdisk-skybox-gone", Tags: tags("gone"), Created: ago(2 * time.Hour)},
			{Kind: azure.KindNIC, ID: "2", Name: "nic-skybox-gone", Tags: tags("gone"), Created: ago(2 * time.Hour)},
			{Kind: azure.KindPublicIP, ID: "3", Name: "pip-skybox-gone", Tags: tags("gone"), Created: ago(2 * time.Hour)},
			{Kind: azure.KindResourceGroup, ID: "4", Name: "rg-skybox-gone", Tags: tags("gone"), Created: ago(2 * time.Hour)},
			{Kind: azure.KindDisk, ID: "5", Name: "osdisk-skybox-new", Tags: tags("new"), Created: ago(10 * time.Minute)},
			{Kind: azure.KindDisk, ID: "6", Name: "osdisk-skybox-alive", Tags: tags("alive"), Created: ago(48 * time.Hour)},
		},
	}

	assertActions(t, Plan(s, DefaultPolicy),
		"delete-leftover disk osdisk-skybox-gone",
		"delete-leftover network-interface nic-skybox-gone",
		"delete-leftover public-ip pip-skybox-gone",
		"delete-leftover resource-group rg-skybox-gone")
}

func TestAResourceGroupHoldingAnythingUntaggedOrAVMIsKept(t *testing.T) {
	tags := map[string]string{"skybox:component": "sandbox", "skybox:sandbox": "gone"}
	s := Snapshot{Now: now, Leftovers: []azure.Leftover{
		{Kind: azure.KindResourceGroup, ID: "1", Name: "rg-a", Tags: tags, Created: ago(48 * time.Hour), Untagged: []string{"someones-db"}},
		{Kind: azure.KindResourceGroup, ID: "2", Name: "rg-b", Tags: tags, Created: ago(48 * time.Hour), HoldsVM: true},
	}}

	assertActions(t, Plan(s, DefaultPolicy))
}

func TestAnAttachedDiskIsKept(t *testing.T) {
	s := Snapshot{Now: now, Leftovers: []azure.Leftover{{Kind: azure.KindDisk, ID: "1", Name: "d",
		Tags: map[string]string{"skybox:component": "sandbox", "skybox:sandbox": "gone"}, Created: ago(48 * time.Hour), Attached: true}}}

	assertActions(t, Plan(s, DefaultPolicy))
}

func TestOutboundAccessGoesOnceNoSandboxRuns(t *testing.T) {
	outbound := []azure.Leftover{
		{Kind: azure.KindNATGateway, ID: "1", Name: "ng", Tags: map[string]string{"skybox:component": "outbound"}, Created: ago(2 * time.Hour)},
		{Kind: azure.KindPublicIP, ID: "2", Name: "pip", Tags: map[string]string{"skybox:component": "outbound"}, Created: ago(2 * time.Hour)},
	}
	sleeping := Snapshot{Now: now, Machines: []azure.Machine{asleep("a", time.Hour)}, Leftovers: outbound}
	running := Snapshot{Now: now, Machines: []azure.Machine{asleep("a", time.Hour), machine("b", sandbox.Waking)}, Leftovers: outbound}

	assertActions(t, Plan(sleeping, DefaultPolicy), "release-outbound outbound access")
	assertActions(t, Plan(running, DefaultPolicy))
}

func TestCloudflareResourcesWhoseSandboxIsGoneAreDeletedPastTheirGrace(t *testing.T) {
	record := func(name, sandbox string, created time.Duration) cloudflare.DNSRecord {
		return cloudflare.DNSRecord{ID: name, Name: name, Content: "t-" + sandbox + ".cfargotunnel.com",
			Comment: "skybox:sandbox=" + sandbox, CreatedOn: ago(created)}
	}
	s := Snapshot{
		Now:      now,
		Machines: []azure.Machine{machine("alive", sandbox.Running)},
		Activity: map[string]azure.Activity{"alive": idle(time.Minute, time.Minute)},
		Tunnels: []cloudflare.NamedTunnel{
			{ID: "t-alive", Name: "skybox-alive", CreatedAt: ago(48 * time.Hour)},
			{ID: "t-stale", Name: "skybox-alive", CreatedAt: ago(48 * time.Hour)},
			{ID: "t-gone", Name: "skybox-gone", CreatedAt: ago(2 * time.Hour)},
			{ID: "t-creating", Name: "skybox-creating", CreatedAt: ago(5 * time.Minute)},
		},
		Records: []cloudflare.DNSRecord{
			record("alive-sbx.example.com", "alive", 48*time.Hour),
			record("3000-alive-sbx.example.com", "alive", 48*time.Hour),
			record("gone-sbx.example.com", "gone", 2*time.Hour),
			record("3000-gone-sbx.example.com", "gone", 2*time.Hour),
			record("creating-sbx.example.com", "creating", 5*time.Minute),
		},
	}

	actions := Plan(s, DefaultPolicy)

	assertActions(t, actions,
		"delete-tunnel skybox-alive",
		"delete-tunnel skybox-gone",
		"delete-dns-records gone")
	if actions[0].Tunnels[0].ID != "t-stale" {
		t.Fatalf("deletes tunnel %s, want the stale one", actions[0].Tunnels[0].ID)
	}
	if len(actions[2].Records) != 2 {
		t.Fatalf("delete-dns-records lists %d records, want 2", len(actions[2].Records))
	}
}

func TestResourcesWithoutASkyboxMarkAreNeverTouched(t *testing.T) {
	s := Snapshot{
		Now: now,
		Leftovers: []azure.Leftover{
			{Kind: azure.KindDisk, ID: "1", Name: "untagged", Created: ago(48 * time.Hour)},
			{Kind: azure.KindResourceGroup, ID: "2", Name: "rg-skybox-tfstate", Tags: map[string]string{"skybox:component": "tfstate"}, Created: ago(48 * time.Hour)},
		},
		Tunnels: []cloudflare.NamedTunnel{{ID: "home", Name: "home-lab", CreatedAt: ago(48 * time.Hour)}},
		Records: []cloudflare.DNSRecord{
			{ID: "1", Name: "blog.example.com", Content: "home.cfargotunnel.com", CreatedOn: ago(48 * time.Hour)},
			{ID: "2", Name: "www.example.com", Content: "example.pages.dev", Comment: "skybox is great", CreatedOn: ago(48 * time.Hour)},
		},
	}

	assertActions(t, Plan(s, DefaultPolicy))
}

func TestOutboundAccessStaysWhileASandboxIsBeingCreated(t *testing.T) {
	s := Snapshot{
		Now:       now,
		Machines:  []azure.Machine{asleep("a", time.Hour)},
		Leftovers: []azure.Leftover{{Kind: azure.KindNATGateway, ID: "1", Name: "ng", Tags: map[string]string{"skybox:component": "outbound"}, Created: ago(48 * time.Hour)}},
		Tunnels:   []cloudflare.NamedTunnel{{ID: "t-new", Name: "skybox-new", CreatedAt: ago(30 * time.Second)}},
	}

	assertActions(t, Plan(s, DefaultPolicy))
}
