package azure

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/richardamare/skybox/internal/sandbox"
)

// The control plane's view of Azure: every sandbox with the tags that decide its fate, how recently a T3 Code thread
// ran on it, and the skybox-tagged resources that may outlive their sandbox.

// TagKeep on a sandbox's VM tells the control plane to leave the sandbox as it is.
const TagKeep = "skybox:keep"

// Machine is a sandbox with what the control plane needs beyond its state.
type Machine struct {
	sandbox.Sandbox
	Keep bool
	// AsleepSince is when the VM last released its disk, which deallocation does. It is zero unless the sandbox is
	// asleep.
	AsleepSince time.Time
}

// Machines lists every sandbox, read fresh, with its keep tag and, for a sleeping one, when it fell asleep.
func (p *Provider) Machines(ctx context.Context) ([]Machine, error) {
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

	machines := make([]Machine, len(names))
	errs := make([]error, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() { machines[i], errs[i] = p.machine(ctx, name) })
	}
	wg.Wait()
	return machines, errors.Join(errs...)
}

func (p *Provider) machine(ctx context.Context, name string) (Machine, error) {
	vm, err := p.vms.Get(ctx, p.cfg.ResourceGroup, vmName(name), &armcompute.VirtualMachinesClientGetOptions{
		Expand: to.Ptr(armcompute.InstanceViewTypesInstanceView),
	})
	if err != nil {
		return Machine{}, err
	}
	_, keep := vm.Tags[TagKeep]
	m := Machine{Sandbox: toSandbox(vm.VirtualMachine), Keep: keep}
	if m.State != sandbox.Asleep {
		return m, nil
	}
	disk, err := p.disks.Get(ctx, p.cfg.ResourceGroup, diskName(name), nil)
	if err != nil {
		return Machine{}, fmt.Errorf("read the disk of %s: %w", name, err)
	}
	if disk.Properties != nil {
		m.AsleepSince = deref(disk.Properties.LastOwnershipUpdateTime)
	}
	return m, nil
}

// Activity is what a sandbox reports about its T3 Code threads.
type Activity struct {
	Booted time.Time `json:"booted"`
	// Running counts the runs going on now, one per thread at most.
	Running int `json:"running"`
	// LatestRun is when the latest run started or ended, zero when no thread ever ran.
	LatestRun time.Time `json:"latestRun"`
}

// IdleSince is the moment from which the sandbox counts as idle when no run goes on: boot or the latest run, whichever
// came later.
func (a Activity) IdleSince() time.Time {
	if a.LatestRun.After(a.Booted) {
		return a.LatestRun
	}
	return a.Booted
}

//go:embed activity.py
var activityScript string

const activityMarker = "skybox-activity "

// Activity asks a running sandbox through Run Command, which needs no code on the VM beyond Python and takes tens of
// seconds.
func (p *Provider) Activity(ctx context.Context, name string) (Activity, error) {
	script := "python3 - <<'SKYBOX_ACTIVITY'\n" + activityScript + "SKYBOX_ACTIVITY\n"
	poller, err := p.vms.BeginRunCommand(ctx, p.cfg.ResourceGroup, vmName(name), armcompute.RunCommandInput{
		CommandID: to.Ptr("RunShellScript"),
		Script:    []*string{to.Ptr(script)},
	}, nil)
	if err != nil {
		return Activity{}, err
	}
	result, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return Activity{}, err
	}
	var output strings.Builder
	for _, status := range result.Value {
		output.WriteString(deref(status.Message))
	}
	return parseActivity(output.String())
}

func parseActivity(output string) (Activity, error) {
	for line := range strings.Lines(output) {
		if body, ok := strings.CutPrefix(strings.TrimSpace(line), activityMarker); ok {
			var report struct {
				Booted    time.Time  `json:"booted"`
				Running   int        `json:"running"`
				LatestRun *time.Time `json:"latestRun"`
			}
			if err := json.Unmarshal([]byte(body), &report); err != nil {
				return Activity{}, fmt.Errorf("the activity report is not JSON: %w", err)
			}
			return Activity{Booted: report.Booted, Running: report.Running, LatestRun: deref(report.LatestRun)}, nil
		}
	}
	return Activity{}, fmt.Errorf("the sandbox printed no activity report: %s", strings.TrimSpace(output))
}

// Leftover kinds the control plane deletes.
const (
	KindDisk          = "disk"
	KindNIC           = "network-interface"
	KindPublicIP      = "public-ip"
	KindNATGateway    = "nat-gateway"
	KindResourceGroup = "resource-group"
)

var leftoverKinds = map[string]string{
	"microsoft.compute/disks":             KindDisk,
	"microsoft.network/networkinterfaces": KindNIC,
	"microsoft.network/publicipaddresses": KindPublicIP,
	"microsoft.network/natgateways":       KindNATGateway,
}

// Leftover is a resource that may outlive its sandbox.
type Leftover struct {
	Kind  string
	ID    string
	Name  string
	Group string
	Tags  map[string]string
	// Created is when the resource was created. For a resource group it is the latest creation of the group or anything
	// in it. Azure moves a resource's change time on its own, so only creation dates a leftover.
	Created time.Time
	// Attached is set for a disk that a VM holds.
	Attached bool
	// Untagged names the resources in a resource group that carry no skybox: tag.
	Untagged []string
	// HoldsVM is set for a resource group with a VM in it.
	HoldsVM bool
}

// Sandbox names the sandbox the resource belongs to, empty for outbound access.
func (l Leftover) Sandbox() string { return l.Tags[tagSandbox] }

// Outbound reports whether the resource is part of the sandboxes' shared outbound access.
func (l Leftover) Outbound() bool { return l.Tags[tagComponent] == componentOutbound }

// Leftovers lists the disks, NICs, public IPs and NAT gateways in the foundation's resource group, and the resource
// groups in the subscription, that carry a skybox:component tag naming a sandbox or outbound access.
func (p *Provider) Leftovers(ctx context.Context) ([]Leftover, error) {
	resources, err := p.resourcesIn(ctx, p.cfg.ResourceGroup)
	if err != nil {
		return nil, err
	}
	leftovers := leftoversIn(p.cfg.ResourceGroup, resources)

	groups, err := p.resourceGroups(ctx)
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		if !ownedComponent(g.Tags) || g.Name == p.cfg.ResourceGroup {
			continue
		}
		leftover := Leftover{Kind: KindResourceGroup, ID: g.ID, Name: g.Name, Group: g.Name, Tags: plainTags(g.Tags), Created: g.CreatedTime}
		contents, err := p.resourcesIn(ctx, g.Name)
		if err != nil {
			return nil, err
		}
		for _, r := range contents {
			leftover.Created = latest(leftover.Created, deref(r.CreatedTime))
			if !hasSkyboxTag(r.Tags) {
				leftover.Untagged = append(leftover.Untagged, deref(r.Name))
			}
			if strings.EqualFold(deref(r.Type), "Microsoft.Compute/virtualMachines") {
				leftover.HoldsVM = true
			}
		}
		leftovers = append(leftovers, leftover)
	}
	return leftovers, nil
}

// leftoversIn keeps the resources of a kind the control plane deletes that carry an owned skybox:component tag.
func leftoversIn(group string, resources []*armresources.GenericResourceExpanded) []Leftover {
	var leftovers []Leftover
	for _, r := range resources {
		kind := leftoverKinds[strings.ToLower(deref(r.Type))]
		if kind == "" || !ownedComponent(r.Tags) {
			continue
		}
		leftovers = append(leftovers, Leftover{
			Kind:     kind,
			ID:       deref(r.ID),
			Name:     deref(r.Name),
			Group:    group,
			Tags:     plainTags(r.Tags),
			Created:  deref(r.CreatedTime),
			Attached: kind == KindDisk && deref(r.ManagedBy) != "",
		})
	}
	return leftovers
}

func (p *Provider) resourcesIn(ctx context.Context, group string) ([]*armresources.GenericResourceExpanded, error) {
	var all []*armresources.GenericResourceExpanded
	pager := p.resources.NewListByResourceGroupPager(group, &armresources.ClientListByResourceGroupOptions{
		Expand: to.Ptr("createdTime"),
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list the resources in %s: %w", group, err)
		}
		all = append(all, page.Value...)
	}
	return all, nil
}

type resourceGroup struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Tags        map[string]*string `json:"tags"`
	CreatedTime time.Time          `json:"createdTime"`
}

// resourceGroups calls the REST API itself, since the SDK cannot ask for each group's creation time.
func (p *Provider) resourceGroups(ctx context.Context) ([]resourceGroup, error) {
	var all []resourceGroup
	next := runtime.JoinPaths(p.arm.Endpoint(), "/subscriptions/"+p.subscription+"/resourcegroups") +
		"?api-version=2021-04-01&$expand=createdTime"
	for next != "" {
		req, err := runtime.NewRequest(ctx, http.MethodGet, next)
		if err != nil {
			return nil, err
		}
		resp, err := p.arm.Pipeline().Do(req)
		if err != nil {
			return nil, err
		}
		if !runtime.HasStatusCode(resp, http.StatusOK) {
			return nil, fmt.Errorf("list resource groups: %w", runtime.NewResponseError(resp))
		}
		var page struct {
			Value    []resourceGroup `json:"value"`
			NextLink string          `json:"nextLink"`
		}
		if err := runtime.UnmarshalAsJSON(resp, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Value...)
		next = page.NextLink
	}
	return all, nil
}

// ErrUnowned refuses an action on a resource that carries no skybox:component tag naming a sandbox or outbound
// access. The foundation and the Terraform state carry other components, so they never qualify.
var ErrUnowned = errors.New("the resource carries no skybox:component tag naming a sandbox or outbound access")

func ownedComponent(tags map[string]*string) bool {
	switch tag(tags, tagComponent) {
	case componentSandbox, componentOutbound:
		return true
	}
	return false
}

func hasSkyboxTag(tags map[string]*string) bool {
	for k := range tags {
		if strings.HasPrefix(k, "skybox:") {
			return true
		}
	}
	return false
}

// DeleteLeftover reads the resource again and deletes it only if it still carries its skybox tag and nothing uses it,
// so a sandbox created under the same name since the listing keeps its parts.
func (p *Provider) DeleteLeftover(ctx context.Context, l Leftover) error {
	switch l.Kind {
	case KindDisk:
		disk, err := p.disks.Get(ctx, l.Group, l.Name, nil)
		if isNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !ownedComponent(disk.Tags) {
			return ErrUnowned
		}
		if deref(disk.ManagedBy) != "" {
			return fmt.Errorf("disk %s is attached to %s", l.Name, deref(disk.ManagedBy))
		}
		poller, err := p.disks.BeginDelete(ctx, l.Group, l.Name, nil)
		return awaitDelete(ctx, poller, err)
	case KindNIC:
		nic, err := p.nics.Get(ctx, l.Group, l.Name, nil)
		if isNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !ownedComponent(nic.Tags) {
			return ErrUnowned
		}
		if nic.Properties != nil && nic.Properties.VirtualMachine != nil {
			return fmt.Errorf("network interface %s is attached to %s", l.Name, deref(nic.Properties.VirtualMachine.ID))
		}
		poller, err := p.nics.BeginDelete(ctx, l.Group, l.Name, nil)
		return awaitDelete(ctx, poller, err)
	case KindPublicIP:
		ip, err := p.publicIPs.Get(ctx, l.Group, l.Name, nil)
		if isNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !ownedComponent(ip.Tags) {
			return ErrUnowned
		}
		if ip.Properties != nil && (ip.Properties.IPConfiguration != nil || ip.Properties.NatGateway != nil) {
			return fmt.Errorf("public IP %s is in use", l.Name)
		}
		poller, err := p.publicIPs.BeginDelete(ctx, l.Group, l.Name, nil)
		return awaitDelete(ctx, poller, err)
	case KindNATGateway:
		nat, err := p.nats.Get(ctx, l.Group, l.Name, nil)
		if isNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !ownedComponent(nat.Tags) {
			return ErrUnowned
		}
		if nat.Properties != nil && len(nat.Properties.Subnets) > 0 {
			return fmt.Errorf("NAT gateway %s still serves a subnet", l.Name)
		}
		poller, err := p.nats.BeginDelete(ctx, l.Group, l.Name, nil)
		return awaitDelete(ctx, poller, err)
	case KindResourceGroup:
		group, err := p.groups.Get(ctx, l.Name, nil)
		if isNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !ownedComponent(group.Tags) || l.Name == p.cfg.ResourceGroup {
			return ErrUnowned
		}
		contents, err := p.resourcesIn(ctx, l.Name)
		if err != nil {
			return err
		}
		for _, r := range contents {
			if !hasSkyboxTag(r.Tags) {
				return fmt.Errorf("resource group %s holds %s, which carries no skybox: tag", l.Name, deref(r.Name))
			}
		}
		poller, err := p.groups.BeginDelete(ctx, l.Name, nil)
		return awaitDelete(ctx, poller, err)
	}
	return fmt.Errorf("the control plane does not delete a %s", l.Kind)
}

func awaitDelete[T any](ctx context.Context, poller *runtime.Poller[T], err error) error {
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return err
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

func plainTags(tags map[string]*string) map[string]string {
	plain := make(map[string]string, len(tags))
	for k, v := range tags {
		plain[k] = deref(v)
	}
	return plain
}

func latest(times ...time.Time) time.Time {
	var max time.Time
	for _, t := range times {
		if t.After(max) {
			max = t
		}
	}
	return max
}
