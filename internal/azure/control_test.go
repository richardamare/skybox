package azure

import (
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

func TestParseActivityReadsTheReportAmongOtherOutput(t *testing.T) {
	output := "Enable succeeded: \n[stdout]\nskybox-activity {\"booted\": \"2026-10-28T10:00:00+00:00\", \"running\": 2, \"latestRun\": \"2026-10-28T11:30:00.5+00:00\"}\n\n[stderr]\n"

	a, err := parseActivity(output)

	if err != nil || a.Running != 2 || !a.Booted.Equal(time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)) ||
		!a.LatestRun.Equal(time.Date(2026, 10, 28, 11, 30, 0, 5e8, time.UTC)) {
		t.Fatalf("parseActivity = %+v, %v", a, err)
	}
}

func TestParseActivityAcceptsASandboxWhereNoThreadEverRan(t *testing.T) {
	a, err := parseActivity("[stdout]\nskybox-activity {\"booted\": \"2026-10-28T10:00:00+00:00\", \"running\": 0, \"latestRun\": null}\n")

	if err != nil || !a.LatestRun.IsZero() || !a.IdleSince().Equal(a.Booted) {
		t.Fatalf("parseActivity = %+v, %v; want idle since boot", a, err)
	}
}

func TestParseActivityFailsWithoutAReport(t *testing.T) {
	_, err := parseActivity("Enable succeeded: \n[stdout]\n\n[stderr]\npython3: command not found\n")

	if err == nil || !strings.Contains(err.Error(), "python3: command not found") {
		t.Fatalf("parseActivity = %v; want an error quoting the output", err)
	}
}

func TestIdleSinceIsTheLaterOfBootAndTheLatestRun(t *testing.T) {
	boot := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	before, after := boot.Add(-time.Hour), boot.Add(time.Hour)

	if got := (Activity{Booted: boot, LatestRun: before}).IdleSince(); !got.Equal(boot) {
		t.Errorf("IdleSince = %s, want the boot", got)
	}
	if got := (Activity{Booted: boot, LatestRun: after}).IdleSince(); !got.Equal(after) {
		t.Errorf("IdleSince = %s, want the latest run", got)
	}
}

func resource(kind, name string, tags map[string]*string) *armresources.GenericResourceExpanded {
	return &armresources.GenericResourceExpanded{ID: to.Ptr("/" + name), Name: to.Ptr(name), Type: to.Ptr(kind), Tags: tags}
}

func TestLeftoversAreOnlySkyboxSandboxAndOutboundResourcesOfDeletableKinds(t *testing.T) {
	sandboxTags := map[string]*string{tagComponent: to.Ptr("sandbox"), tagSandbox: to.Ptr("gone")}
	resources := []*armresources.GenericResourceExpanded{
		resource("Microsoft.Compute/disks", "osdisk-skybox-gone", sandboxTags),
		resource("Microsoft.Network/networkInterfaces", "nic-skybox-gone", sandboxTags),
		resource("Microsoft.Network/publicIPAddresses", "pip-skybox-prod-gwc-outbound", map[string]*string{tagComponent: to.Ptr("outbound")}),
		resource("Microsoft.Network/natGateways", "ng-skybox-prod-gwc-outbound", map[string]*string{tagComponent: to.Ptr("outbound")}),
		resource("Microsoft.Compute/virtualMachines", "vm-skybox-gone", sandboxTags),
		resource("Microsoft.Compute/disks", "someone-elses-disk", nil),
		resource("Microsoft.Network/virtualNetworks", "vnet-skybox-prod-gwc", map[string]*string{tagComponent: to.Ptr("foundation")}),
		resource("Microsoft.Network/publicIPAddresses", "foundation-ip", map[string]*string{tagComponent: to.Ptr("foundation")}),
		resource("Microsoft.Compute/disks", "other-skybox-disk", map[string]*string{"skybox:note": to.Ptr("hand-made")}),
	}

	var got []string
	for _, l := range leftoversIn("rg", resources) {
		got = append(got, l.Kind+" "+l.Name)
	}

	want := "disk osdisk-skybox-gone,network-interface nic-skybox-gone,public-ip pip-skybox-prod-gwc-outbound,nat-gateway ng-skybox-prod-gwc-outbound"
	if strings.Join(got, ",") != want {
		t.Fatalf("leftovers = %q, want %q", got, want)
	}
}

func TestADiskAVMHoldsIsAttached(t *testing.T) {
	disk := resource("Microsoft.Compute/disks", "osdisk-skybox-a", map[string]*string{tagComponent: to.Ptr("sandbox"), tagSandbox: to.Ptr("a")})
	disk.ManagedBy = to.Ptr("/vm-skybox-a")

	if l := leftoversIn("rg", []*armresources.GenericResourceExpanded{disk}); len(l) != 1 || !l[0].Attached {
		t.Fatalf("leftovers = %+v; want one attached disk", l)
	}
}
