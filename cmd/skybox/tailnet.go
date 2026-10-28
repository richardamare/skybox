package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/richardamare/skybox/internal/tailscale"
)

// tailnet is the part of Tailscale a sandbox needs: the auth key it joins with, and its device once it has joined.
type tailnet interface {
	MintAuthKey(ctx context.Context, sandbox string) (tailscale.AuthKey, error)
	RevokeAuthKey(ctx context.Context, id string) error
	Devices(ctx context.Context, sandbox string) ([]tailscale.Device, error)
	DeleteDevices(ctx context.Context, sandbox string) error
}

// waitForTailnet returns the sandbox's device once it has joined the tailnet. cloud-init joins before it starts the
// tunnel, so the device is there or about to be by the time the tunnel connects.
func (d deps) waitForTailnet(ctx context.Context, cmd *cobra.Command, ts tailnet, name string) (tailscale.Device, error) {
	progress(cmd, "waiting for %s to join the tailnet", name)
	var device tailscale.Device
	err := poll(ctx, d.joinTimeout, d.pollInterval, func() (bool, error) {
		devices, err := ts.Devices(ctx, name)
		if err != nil || len(devices) == 0 {
			return false, err
		}
		device = devices[0]
		return true, nil
	})
	if errors.Is(err, errTimedOut) {
		return tailscale.Device{}, fmt.Errorf("%s connected its tunnel but did not join the tailnet within %s: it is left as it is, so read its boot log with `az vm boot-diagnostics get-boot-log`, or delete it with skybox down %s", name, d.joinTimeout, name)
	}
	return device, err
}
