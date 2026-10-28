package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

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

// waitForServe waits until T3 Code answers at the sandbox's tailnet address, which happens only once Tailscale Serve
// publishes it. Serve stays silent, not failing, when it is off for the tailnet.
func (d deps) waitForServe(ctx context.Context, cmd *cobra.Command, name, url string) error {
	progress(cmd, "waiting for T3 Code on %s to answer at %s", name, url)
	err := poll(ctx, d.joinTimeout, d.pollInterval, func() (bool, error) {
		return d.answers(ctx, url), nil
	})
	if errors.Is(err, errTimedOut) {
		return fmt.Errorf("%s joined the tailnet but T3 Code does not answer at %s within %s: check that Tailscale Serve and HTTPS certificates are on for the tailnet (ADR 0006), then run `systemctl --user restart t3code` on %s, or delete it with skybox down %s", name, url, d.joinTimeout, name, name)
	}
	return err
}

func answersHTTP(ctx context.Context, url string) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}
