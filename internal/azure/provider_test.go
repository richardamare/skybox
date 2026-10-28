package azure

import (
	"errors"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v9"

	"github.com/richardamare/skybox/internal/sandbox"
)

func TestEachSizeRunsOnItsVMSize(t *testing.T) {
	want := map[sandbox.Size]string{
		sandbox.Light:    "Standard_D2als_v7",
		sandbox.Standard: "Standard_D4as_v7",
		sandbox.Large:    "Standard_D8as_v7",
	}
	for _, size := range sandbox.Sizes {
		if got := machines[size].vmSize; got != want[size] {
			t.Errorf("%s runs on %q, want %q", size, got, want[size])
		}
	}
}

func usage(name string, current int32, limit int64) *armcompute.Usage {
	return &armcompute.Usage{Name: &armcompute.UsageName{Value: to.Ptr(name)}, CurrentValue: to.Ptr(current), Limit: to.Ptr(limit)}
}

func TestCapacityErrorNamesTheFamilyWithoutQuota(t *testing.T) {
	usages := []*armcompute.Usage{usage("cores", 6, 10), usage("StandardDasv7Family", 8, 10)}
	err := capacityError("germanywestcentral", sandbox.Standard, machines[sandbox.Standard], usages)

	var capacity *sandbox.CapacityError
	if !errors.As(err, &capacity) || capacity.Family != "StandardDasv7Family" || capacity.Free != 2 || capacity.Need != 4 {
		t.Fatalf("capacityError = %v; want the Dasv7 family with 2 free of 4 needed", err)
	}
	if !strings.Contains(err.Error(), "StandardDasv7Family") {
		t.Fatalf("message %q does not name the family", err)
	}
}

func TestCapacityErrorChecksTheRegionalTotal(t *testing.T) {
	usages := []*armcompute.Usage{usage("cores", 8, 10), usage("StandardDasv7Family", 0, 10)}
	err := capacityError("germanywestcentral", sandbox.Standard, machines[sandbox.Standard], usages)

	var capacity *sandbox.CapacityError
	if !errors.As(err, &capacity) || capacity.Family != "regional" {
		t.Fatalf("capacityError = %v; want the regional total", err)
	}
}

func TestCapacityIsEnoughWhenBothQuotasHaveRoom(t *testing.T) {
	usages := []*armcompute.Usage{usage("cores", 6, 10), usage("StandardDalsv7Family", 8, 10)}
	if err := capacityError("germanywestcentral", sandbox.Light, machines[sandbox.Light], usages); err != nil {
		t.Fatalf("capacityError = %v; want nil", err)
	}
}

func statuses(codes ...string) []*armcompute.InstanceViewStatus {
	var out []*armcompute.InstanceViewStatus
	for _, code := range codes {
		out = append(out, &armcompute.InstanceViewStatus{Code: to.Ptr(code)})
	}
	return out
}

func TestStateReadsTheInstanceView(t *testing.T) {
	for _, tc := range []struct {
		codes []string
		want  sandbox.State
	}{
		{[]string{"ProvisioningState/succeeded", "PowerState/running"}, sandbox.Running},
		{[]string{"ProvisioningState/succeeded", "PowerState/deallocated"}, sandbox.Asleep},
		{[]string{"ProvisioningState/updating", "PowerState/deallocating"}, sandbox.Sleeping},
		{[]string{"ProvisioningState/updating", "PowerState/starting"}, sandbox.Waking},
		{[]string{"ProvisioningState/creating"}, sandbox.Creating},
		{[]string{"ProvisioningState/failed/OSProvisioningTimedOut", "PowerState/running"}, sandbox.Failed},
		{[]string{"ProvisioningState/deleting", "PowerState/running"}, sandbox.Deleting},
	} {
		if got := state(statuses(tc.codes...)); got != tc.want {
			t.Errorf("state(%v) = %s, want %s", tc.codes, got, tc.want)
		}
	}
}

func TestSandboxReadsBackItsTags(t *testing.T) {
	spec := sandbox.Spec{Name: "api-fix", Size: sandbox.Light, Labels: map[string]string{"tunnel-id": "abc"}}
	vm := armcompute.VirtualMachine{
		Tags: sandboxTags(spec),
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{VMSize: to.Ptr(armcompute.VirtualMachineSizeTypes("Standard_B2s"))},
			InstanceView:    &armcompute.VirtualMachineInstanceView{Statuses: statuses("ProvisioningState/succeeded", "PowerState/running")},
		},
	}
	got := toSandbox(vm)

	if got.Name != "api-fix" || got.Size != sandbox.Light || got.MachineType != "Standard_B2s" || got.State != sandbox.Running || got.Labels["tunnel-id"] != "abc" {
		t.Fatalf("toSandbox = %+v", got)
	}
}

func TestNetworkInterfaceHasNoPublicIP(t *testing.T) {
	nic := networkInterface("germanywestcentral", nil, "subnet-id")

	for _, ip := range nic.Properties.IPConfigurations {
		if ip.Properties.PublicIPAddress != nil {
			t.Fatal("the sandbox NIC has a public IP")
		}
	}
	if !*nic.Properties.EnableAcceleratedNetworking {
		t.Fatal("a v7 NIC needs accelerated networking")
	}
}

func TestVirtualMachineCarriesTheSandboxIdentityAndDeletesItsDiskAndNIC(t *testing.T) {
	spec := sandbox.Spec{Name: "api-fix", Size: sandbox.Standard, SSHKeys: []string{"ssh-ed25519 AAAA one", "sk-ssh-ed25519@openssh.com AAAA two"}}
	vm := virtualMachine("germanywestcentral", spec, machines[sandbox.Standard], sandboxTags(spec), "identity-id", "nic-id", []byte("#cloud-config"))
	props := vm.Properties

	if _, ok := vm.Identity.UserAssignedIdentities["identity-id"]; !ok {
		t.Error("the VM does not carry the sandbox identity")
	}
	if *props.StorageProfile.OSDisk.DeleteOption != armcompute.DiskDeleteOptionTypesDelete {
		t.Error("deleting the VM keeps its disk")
	}
	if *props.NetworkProfile.NetworkInterfaces[0].Properties.DeleteOption != armcompute.DeleteOptionsDelete {
		t.Error("deleting the VM keeps its NIC")
	}
	if *props.StorageProfile.DiskControllerType != armcompute.DiskControllerTypesNVMe {
		t.Error("a v7 VM needs the NVMe disk controller")
	}
	if keys := props.OSProfile.LinuxConfiguration.SSH.PublicKeys; len(keys) != 1 || *keys[0].KeyData != "ssh-ed25519 AAAA one" {
		t.Errorf("admin keys = %d; want only the Ed25519 key Azure accepts", len(keys))
	}
}
