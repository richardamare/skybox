package azure

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v9"

	"github.com/richardamare/skybox/internal/sandbox"
)

// The sandbox subnet has no default outbound access, and a NAT gateway costs about as much per hour as a light
// sandbox. skybox therefore creates the gateway when a sandbox starts and deletes it once no sandbox runs.

func (p *Provider) natName() string      { return "ng-" + p.cfg.Outbound }
func (p *Provider) publicIPName() string { return "pip-" + p.cfg.Outbound }

func outboundTags() map[string]*string {
	return map[string]*string{tagComponent: to.Ptr(componentOutbound)}
}

func (p *Provider) ensureOutbound(ctx context.Context) error {
	ipPoller, err := p.publicIPs.BeginCreateOrUpdate(ctx, p.cfg.ResourceGroup, p.publicIPName(), armnetwork.PublicIPAddress{
		Location: to.Ptr(p.cfg.Location),
		Tags:     outboundTags(),
		SKU:      &armnetwork.PublicIPAddressSKU{Name: to.Ptr(armnetwork.PublicIPAddressSKUNameStandard)},
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
			PublicIPAddressVersion:   to.Ptr(armnetwork.IPVersionIPv4),
		},
	}, nil)
	if err != nil {
		return err
	}
	ip, err := ipPoller.PollUntilDone(ctx, nil)
	if err != nil {
		return err
	}

	natPoller, err := p.nats.BeginCreateOrUpdate(ctx, p.cfg.ResourceGroup, p.natName(), armnetwork.NatGateway{
		Location: to.Ptr(p.cfg.Location),
		Tags:     outboundTags(),
		SKU:      &armnetwork.NatGatewaySKU{Name: to.Ptr(armnetwork.NatGatewaySKUNameStandard)},
		Properties: &armnetwork.NatGatewayPropertiesFormat{
			PublicIPAddresses:    []*armnetwork.SubResource{{ID: ip.ID}},
			IdleTimeoutInMinutes: to.Ptr[int32](10),
		},
	}, nil)
	if err != nil {
		return err
	}
	nat, err := natPoller.PollUntilDone(ctx, nil)
	if err != nil {
		return err
	}

	subnet, err := p.subnets.Get(ctx, p.cfg.ResourceGroup, p.cfg.VirtualNetwork, p.cfg.Subnet, nil)
	if err != nil {
		return err
	}
	if current := subnet.Properties.NatGateway; current != nil && deref(current.ID) == deref(nat.ID) {
		return nil
	}
	subnet.Properties.NatGateway = &armnetwork.SubResource{ID: nat.ID}
	return p.putSubnet(ctx, subnet.Subnet)
}

// releaseOutboundIfIdle deletes the NAT gateway and its IP when every sandbox is asleep or failed.
func (p *Provider) releaseOutboundIfIdle(ctx context.Context) error {
	sandboxes, err := p.List(ctx)
	if err != nil {
		return err
	}
	for _, s := range sandboxes {
		if s.State != sandbox.Asleep && s.State != sandbox.Failed {
			return nil
		}
	}

	subnet, err := p.subnets.Get(ctx, p.cfg.ResourceGroup, p.cfg.VirtualNetwork, p.cfg.Subnet, nil)
	if err != nil {
		return err
	}
	if subnet.Properties.NatGateway != nil {
		subnet.Properties.NatGateway = nil
		if err := p.putSubnet(ctx, subnet.Subnet); err != nil {
			return err
		}
	}
	if natPoller, err := p.nats.BeginDelete(ctx, p.cfg.ResourceGroup, p.natName(), nil); err != nil {
		if !isNotFound(err) {
			return err
		}
	} else if _, err := natPoller.PollUntilDone(ctx, nil); err != nil && !isNotFound(err) {
		return err
	}
	if ipPoller, err := p.publicIPs.BeginDelete(ctx, p.cfg.ResourceGroup, p.publicIPName(), nil); err != nil {
		if !isNotFound(err) {
			return err
		}
	} else if _, err := ipPoller.PollUntilDone(ctx, nil); err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

func (p *Provider) putSubnet(ctx context.Context, subnet armnetwork.Subnet) error {
	poller, err := p.subnets.BeginCreateOrUpdate(ctx, p.cfg.ResourceGroup, p.cfg.VirtualNetwork, p.cfg.Subnet, subnet, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}
