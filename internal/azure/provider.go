// Package azure is the provider that runs sandboxes as Azure VMs in the foundation's resource group.
package azure

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	"github.com/richardamare/skybox/internal/sandbox"
)

// Config names the foundation that deploy/envs/prod creates. The subscription is found at runtime, as the one that
// holds ResourceGroup.
type Config struct {
	ResourceGroup  string
	Location       string
	VirtualNetwork string
	Subnet         string
	Identity       string
	// Outbound names the NAT gateway and its public IP that give sandboxes internet access while any of them runs.
	Outbound string
}

var Prod = Config{
	ResourceGroup:  "rg-skybox-prod-gwc",
	Location:       "germanywestcentral",
	VirtualNetwork: "vnet-skybox-prod-gwc",
	Subnet:         "snet-sandboxes",
	Identity:       "id-skybox-sandbox-gwc",
	Outbound:       "skybox-prod-gwc-outbound",
}

const (
	tagComponent = "skybox:component"
	tagSandbox   = "skybox:sandbox"
	tagSize      = "skybox:size"
	labelPrefix  = "skybox:label:"

	componentFoundation = "foundation"
	componentSandbox    = "sandbox"
	componentOutbound   = "outbound"

	adminUser = "skybox"
)

type Provider struct {
	cfg          Config
	subscription string
	arm          *arm.Client
	resources    *armresources.Client
	groups       *armresources.ResourceGroupsClient
	vms          *armcompute.VirtualMachinesClient
	disks        *armcompute.DisksClient
	usage        *armcompute.UsageClient
	nics         *armnetwork.InterfacesClient
	subnets      *armnetwork.SubnetsClient
	nats         *armnetwork.NatGatewaysClient
	publicIPs    *armnetwork.PublicIPAddressesClient
	vaults       *armkeyvault.VaultsClient
	identities   *armmsi.UserAssignedIdentitiesClient
}

func New(ctx context.Context, cfg Config, cred azcore.TokenCredential) (*Provider, error) {
	subscription, err := findSubscription(ctx, cfg.ResourceGroup, cred)
	if err != nil {
		return nil, err
	}
	compute, err := armcompute.NewClientFactory(subscription, cred, nil)
	if err != nil {
		return nil, err
	}
	network, err := armnetwork.NewClientFactory(subscription, cred, nil)
	if err != nil {
		return nil, err
	}
	vaults, err := armkeyvault.NewVaultsClient(subscription, cred, nil)
	if err != nil {
		return nil, err
	}
	identities, err := armmsi.NewUserAssignedIdentitiesClient(subscription, cred, nil)
	if err != nil {
		return nil, err
	}
	resources, err := armresources.NewClientFactory(subscription, cred, nil)
	if err != nil {
		return nil, err
	}
	armClient, err := arm.NewClient("skybox", "v0.0.0", cred, nil)
	if err != nil {
		return nil, err
	}
	return &Provider{
		cfg:          cfg,
		subscription: subscription,
		arm:          armClient,
		resources:    resources.NewClient(),
		groups:       resources.NewResourceGroupsClient(),
		vms:          compute.NewVirtualMachinesClient(),
		disks:        compute.NewDisksClient(),
		usage:        compute.NewUsageClient(),
		nics:         network.NewInterfacesClient(),
		subnets:      network.NewSubnetsClient(),
		nats:         network.NewNatGatewaysClient(),
		publicIPs:    network.NewPublicIPAddressesClient(),
		vaults:       vaults,
		identities:   identities,
	}, nil
}

// findSubscription asks every subscription the credential reaches, all at once, for the foundation's resource group,
// and accepts it only with the foundation's tag.
func findSubscription(ctx context.Context, resourceGroup string, cred azcore.TokenCredential) (string, error) {
	subscriptions, err := armsubscriptions.NewClient(cred, nil)
	if err != nil {
		return "", err
	}
	var ids []string
	pager := subscriptions.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("list Azure subscriptions: %w", err)
		}
		for _, s := range page.Value {
			if deref(s.State) == armsubscriptions.SubscriptionStateEnabled {
				ids = append(ids, deref(s.SubscriptionID))
			}
		}
	}

	found := make([]bool, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Go(func() {
			groups, err := armresources.NewResourceGroupsClient(id, cred, nil)
			if err != nil {
				return
			}
			group, err := groups.Get(ctx, resourceGroup, nil)
			found[i] = err == nil && tag(group.Tags, tagComponent) == componentFoundation
		})
	}
	wg.Wait()
	for i, ok := range found {
		if ok {
			return ids[i], nil
		}
	}
	return "", fmt.Errorf("no Azure subscription you can reach holds the resource group %s: sign in with az login, or apply deploy/envs/prod", resourceGroup)
}

func vmName(name string) string   { return "vm-skybox-" + name }
func nicName(name string) string  { return "nic-skybox-" + name }
func diskName(name string) string { return "osdisk-skybox-" + name }

func (p *Provider) Get(ctx context.Context, name string) (sandbox.Sandbox, error) {
	resp, err := p.vms.Get(ctx, p.cfg.ResourceGroup, vmName(name), &armcompute.VirtualMachinesClientGetOptions{
		Expand: to.Ptr(armcompute.InstanceViewTypesInstanceView),
	})
	if isNotFound(err) {
		return sandbox.Sandbox{}, &sandbox.NotFoundError{Name: name}
	}
	if err != nil {
		return sandbox.Sandbox{}, err
	}
	if tag(resp.Tags, tagComponent) != componentSandbox {
		return sandbox.Sandbox{}, &sandbox.NotFoundError{Name: name}
	}
	return toSandbox(resp.VirtualMachine), nil
}

func (p *Provider) List(ctx context.Context) ([]sandbox.Sandbox, error) {
	var names []string
	pager := p.vms.NewListPager(p.cfg.ResourceGroup, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, vm := range page.Value {
			if tag(vm.Tags, tagComponent) == componentSandbox {
				names = append(names, tag(vm.Tags, tagSandbox))
			}
		}
	}

	// The list call carries no power state, so each sandbox is read on its own, all at once.
	sandboxes := make([]sandbox.Sandbox, len(names))
	errs := make([]error, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() {
			sandboxes[i], errs[i] = p.Get(ctx, name)
		})
	}
	wg.Wait()

	var found []sandbox.Sandbox
	for i, err := range errs {
		var notFound *sandbox.NotFoundError
		switch {
		case errors.As(err, &notFound):
		case err != nil:
			return nil, err
		default:
			found = append(found, sandboxes[i])
		}
	}
	return found, nil
}

func (p *Provider) CheckCapacity(ctx context.Context, size sandbox.Size) error {
	m := machines[size]
	var usages []*armcompute.Usage
	pager := p.usage.NewListPager(p.cfg.Location, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		usages = append(usages, page.Value...)
	}
	return capacityError(p.cfg.Location, size, m, usages)
}

// capacityError checks the family's quota and the regional total, which both cap a new VM.
func capacityError(region string, size sandbox.Size, m machine, usages []*armcompute.Usage) error {
	for _, family := range []string{m.family, "cores"} {
		for _, u := range usages {
			if u.Name == nil || deref(u.Name.Value) != family {
				continue
			}
			free := int(deref(u.Limit)) - int(deref(u.CurrentValue))
			if free < m.vCPUs {
				name := family
				if family == "cores" {
					name = "regional"
				}
				return &sandbox.CapacityError{Size: size, Region: region, Family: name, Free: free, Need: m.vCPUs}
			}
		}
	}
	return nil
}

func (p *Provider) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	m, ok := machines[spec.Size]
	if !ok {
		return sandbox.Sandbox{}, fmt.Errorf("azure has no machine for size %q", spec.Size)
	}
	vault, identity, err := p.foundation(ctx)
	if err != nil {
		return sandbox.Sandbox{}, err
	}
	userData, err := renderCloudInit(cloudInitData{
		SSHKeys:          spec.SSHKeys,
		TunnelToken:      spec.TunnelToken,
		KeyVaultName:     vault,
		IdentityClientID: deref(identity.Properties.ClientID),
		T3Version:        t3Version,
	})
	if err != nil {
		return sandbox.Sandbox{}, err
	}
	if err := p.ensureOutbound(ctx); err != nil {
		return sandbox.Sandbox{}, fmt.Errorf("give sandboxes outbound internet access: %w", err)
	}

	created, err := p.create(ctx, spec, m, deref(identity.ID), userData)
	if err != nil {
		// A half-created sandbox would hold its name and its quota, so it goes before the error is reported.
		if cleanupErr := p.deleteResources(context.WithoutCancel(ctx), spec.Name); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("clean up the failed sandbox: %w", cleanupErr))
		}
		return sandbox.Sandbox{}, err
	}
	return created, nil
}

func (p *Provider) create(ctx context.Context, spec sandbox.Spec, m machine, identityID string, userData []byte) (sandbox.Sandbox, error) {
	tags := sandboxTags(spec)

	subnet, err := p.subnets.Get(ctx, p.cfg.ResourceGroup, p.cfg.VirtualNetwork, p.cfg.Subnet, nil)
	if err != nil {
		return sandbox.Sandbox{}, err
	}
	nicPoller, err := p.nics.BeginCreateOrUpdate(ctx, p.cfg.ResourceGroup, nicName(spec.Name),
		networkInterface(p.cfg.Location, tags, deref(subnet.ID)), nil)
	if err != nil {
		return sandbox.Sandbox{}, err
	}
	nic, err := nicPoller.PollUntilDone(ctx, nil)
	if err != nil {
		return sandbox.Sandbox{}, err
	}

	vmPoller, err := p.vms.BeginCreateOrUpdate(ctx, p.cfg.ResourceGroup, vmName(spec.Name),
		virtualMachine(p.cfg.Location, spec, m, tags, identityID, deref(nic.ID), userData), nil)
	if err != nil {
		return sandbox.Sandbox{}, err
	}
	if _, err := vmPoller.PollUntilDone(ctx, nil); err != nil {
		return sandbox.Sandbox{}, err
	}

	// A disk created with its VM gets none of the VM's tags, and the control plane finds orphans by tag.
	diskPoller, err := p.disks.BeginUpdate(ctx, p.cfg.ResourceGroup, diskName(spec.Name), armcompute.DiskUpdate{Tags: tags}, nil)
	if err != nil {
		return sandbox.Sandbox{}, err
	}
	if _, err := diskPoller.PollUntilDone(ctx, nil); err != nil {
		return sandbox.Sandbox{}, err
	}
	return p.Get(ctx, spec.Name)
}

// networkInterface has a private address only. The sandbox is reached through its outbound tunnel.
func networkInterface(location string, tags map[string]*string, subnetID string) armnetwork.Interface {
	return armnetwork.Interface{
		Location: to.Ptr(location),
		Tags:     tags,
		Properties: &armnetwork.InterfacePropertiesFormat{
			EnableAcceleratedNetworking: to.Ptr(true),
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
				Name: to.Ptr("ipconfig"),
				Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
					Subnet:                    &armnetwork.Subnet{ID: to.Ptr(subnetID)},
					PrivateIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodDynamic),
				},
			}},
		},
	}
}

func virtualMachine(location string, spec sandbox.Spec, m machine, tags map[string]*string, identityID, nicID string, userData []byte) armcompute.VirtualMachine {
	// Azure requires a key for the admin account and accepts only RSA and Ed25519 keys there. The agent account
	// gets every key through cloud-init.
	var keys []*armcompute.SSHPublicKey
	for _, key := range spec.SSHKeys {
		if strings.HasPrefix(key, "ssh-rsa ") || strings.HasPrefix(key, "ssh-ed25519 ") {
			keys = append(keys, &armcompute.SSHPublicKey{
				Path:    to.Ptr("/home/" + adminUser + "/.ssh/authorized_keys"),
				KeyData: to.Ptr(key),
			})
		}
	}
	return armcompute.VirtualMachine{
		Location: to.Ptr(location),
		Tags:     tags,
		Identity: &armcompute.VirtualMachineIdentity{
			Type:                   to.Ptr(armcompute.ResourceIdentityTypeUserAssigned),
			UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{identityID: {}},
		},
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{VMSize: to.Ptr(armcompute.VirtualMachineSizeTypes(m.vmSize))},
			StorageProfile: &armcompute.StorageProfile{
				DiskControllerType: to.Ptr(armcompute.DiskControllerTypesNVMe),
				ImageReference: &armcompute.ImageReference{
					Publisher: to.Ptr("Canonical"),
					Offer:     to.Ptr("ubuntu-24_04-lts"),
					SKU:       to.Ptr("server"),
					Version:   to.Ptr("latest"),
				},
				OSDisk: &armcompute.OSDisk{
					Name:         to.Ptr(diskName(spec.Name)),
					CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesFromImage),
					DeleteOption: to.Ptr(armcompute.DiskDeleteOptionTypesDelete),
					DiskSizeGB:   to.Ptr[int32](64),
					ManagedDisk: &armcompute.ManagedDiskParameters{
						StorageAccountType: to.Ptr(armcompute.StorageAccountTypesStandardSSDLRS),
					},
				},
			},
			OSProfile: &armcompute.OSProfile{
				ComputerName:  to.Ptr(spec.Name),
				AdminUsername: to.Ptr(adminUser),
				CustomData:    to.Ptr(base64.StdEncoding.EncodeToString(userData)),
				LinuxConfiguration: &armcompute.LinuxConfiguration{
					DisablePasswordAuthentication: to.Ptr(true),
					SSH:                           &armcompute.SSHConfiguration{PublicKeys: keys},
				},
			},
			NetworkProfile: &armcompute.NetworkProfile{
				NetworkInterfaces: []*armcompute.NetworkInterfaceReference{{
					ID: to.Ptr(nicID),
					Properties: &armcompute.NetworkInterfaceReferenceProperties{
						Primary:      to.Ptr(true),
						DeleteOption: to.Ptr(armcompute.DeleteOptionsDelete),
					},
				}},
			},
			DiagnosticsProfile: &armcompute.DiagnosticsProfile{
				BootDiagnostics: &armcompute.BootDiagnostics{Enabled: to.Ptr(true)},
			},
		},
	}
}

func (p *Provider) Sleep(ctx context.Context, name string) error {
	if _, err := p.Get(ctx, name); err != nil {
		return err
	}
	poller, err := p.vms.BeginDeallocate(ctx, p.cfg.ResourceGroup, vmName(name), nil)
	if err != nil {
		return err
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return err
	}
	return p.ReleaseOutboundIfIdle(ctx)
}

func (p *Provider) Wake(ctx context.Context, name string) error {
	if _, err := p.Get(ctx, name); err != nil {
		return err
	}
	if err := p.ensureOutbound(ctx); err != nil {
		return fmt.Errorf("give sandboxes outbound internet access: %w", err)
	}
	poller, err := p.vms.BeginStart(ctx, p.cfg.ResourceGroup, vmName(name), nil)
	if err != nil {
		return err
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return err
	}
	// A sleep that read no running sandbox may have removed the outbound path while this one was starting.
	return p.ensureOutbound(ctx)
}

func (p *Provider) Delete(ctx context.Context, name string) error {
	if _, err := p.Get(ctx, name); err != nil {
		return err
	}
	if err := p.deleteResources(ctx, name); err != nil {
		return err
	}
	return p.ReleaseOutboundIfIdle(ctx)
}

// deleteResources removes the VM, then its NIC and disk, each only if it exists, so it also cleans up a sandbox
// whose creation stopped halfway.
func (p *Provider) deleteResources(ctx context.Context, name string) error {
	if vmPoller, err := p.vms.BeginDelete(ctx, p.cfg.ResourceGroup, vmName(name), nil); err != nil {
		if !isNotFound(err) {
			return err
		}
	} else if _, err := vmPoller.PollUntilDone(ctx, nil); err != nil && !isNotFound(err) {
		return err
	}
	if nicPoller, err := p.nics.BeginDelete(ctx, p.cfg.ResourceGroup, nicName(name), nil); err != nil {
		if !isNotFound(err) {
			return err
		}
	} else if _, err := nicPoller.PollUntilDone(ctx, nil); err != nil && !isNotFound(err) {
		return err
	}
	if diskPoller, err := p.disks.BeginDelete(ctx, p.cfg.ResourceGroup, diskName(name), nil); err != nil {
		if !isNotFound(err) {
			return err
		}
	} else if _, err := diskPoller.PollUntilDone(ctx, nil); err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

// foundation finds the Key Vault, whose name Terraform suffixes at random, and the identity every sandbox carries.
func (p *Provider) foundation(ctx context.Context) (string, armmsi.Identity, error) {
	identity, err := p.identities.Get(ctx, p.cfg.ResourceGroup, p.cfg.Identity, nil)
	if err != nil {
		return "", armmsi.Identity{}, fmt.Errorf("read the sandbox identity %s: %w", p.cfg.Identity, err)
	}
	pager := p.vaults.NewListByResourceGroupPager(p.cfg.ResourceGroup, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", armmsi.Identity{}, err
		}
		for _, vault := range page.Value {
			if vault.Name != nil {
				return *vault.Name, identity.Identity, nil
			}
		}
	}
	return "", armmsi.Identity{}, fmt.Errorf("%s holds no Key Vault; apply deploy/envs/prod first", p.cfg.ResourceGroup)
}

func sandboxTags(spec sandbox.Spec) map[string]*string {
	tags := map[string]*string{
		tagComponent: to.Ptr(componentSandbox),
		tagSandbox:   to.Ptr(spec.Name),
		tagSize:      to.Ptr(string(spec.Size)),
	}
	for k, v := range spec.Labels {
		tags[labelPrefix+k] = to.Ptr(v)
	}
	return tags
}

func toSandbox(vm armcompute.VirtualMachine) sandbox.Sandbox {
	labels := map[string]string{}
	for k, v := range vm.Tags {
		if label, ok := strings.CutPrefix(k, labelPrefix); ok && v != nil {
			labels[label] = *v
		}
	}
	var machineType string
	if vm.Properties != nil && vm.Properties.HardwareProfile != nil {
		machineType = string(deref(vm.Properties.HardwareProfile.VMSize))
	}
	var statuses []*armcompute.InstanceViewStatus
	if vm.Properties != nil && vm.Properties.InstanceView != nil {
		statuses = vm.Properties.InstanceView.Statuses
	}
	return sandbox.Sandbox{
		Name:        tag(vm.Tags, tagSandbox),
		Size:        sandbox.Size(tag(vm.Tags, tagSize)),
		MachineType: machineType,
		State:       state(statuses),
		Labels:      labels,
	}
}

// state reads the instance view's ProvisioningState/* and PowerState/* codes.
func state(statuses []*armcompute.InstanceViewStatus) sandbox.State {
	var provisioning, power string
	for _, s := range statuses {
		code := deref(s.Code)
		// A failure carries its reason after another slash: ProvisioningState/failed/OSProvisioningTimedOut.
		if v, ok := strings.CutPrefix(code, "ProvisioningState/"); ok {
			provisioning, _, _ = strings.Cut(v, "/")
		}
		if v, ok := strings.CutPrefix(code, "PowerState/"); ok {
			power = v
		}
	}
	switch {
	case provisioning == "failed":
		return sandbox.Failed
	case provisioning == "deleting":
		return sandbox.Deleting
	case provisioning == "creating" || power == "":
		return sandbox.Creating
	}
	switch power {
	case "running":
		return sandbox.Running
	case "starting":
		return sandbox.Waking
	case "deallocating", "stopping":
		return sandbox.Sleeping
	default:
		return sandbox.Asleep
	}
}

func tag(tags map[string]*string, key string) string {
	return deref(tags[key])
}

func isNotFound(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
