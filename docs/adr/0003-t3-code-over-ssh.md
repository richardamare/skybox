# The T3 Code desktop reaches a sandbox over SSH, not T3 Connect

A sandbox has no public IP and no open port. Each one runs its own Cloudflare Tunnel behind Cloudflare Access, and `skybox up` writes a `skybox-<name>` alias to `~/.ssh/config` whose `ProxyCommand` is `skybox proxy`. The user adds that alias in T3 Code as an SSH environment and picks it in Run on. The user also links it under Linked environments, so agents on the Mac can start and delegate work there.

## Considered Options

- **T3 Connect.** Its free tier allows 3 machines with a live tunnel at once, and more needs a paid T3 Chat plan. Each machine also needs a device approval.
- **Tailscale with pairing links.** No machine limit, but it adds Tailscale to every sandbox and the Mac for what Cloudflare already provides.
- **Claude Code Remote Control.** It works from claude.ai/code and the mobile app without a tunnel, but drops T3 Code's orchestration, previews and other providers. It stays available on every sandbox as a separate way in.

## Consequences

- `skybox proxy` fails at once for a sleeping or missing sandbox and never wakes or creates one. T3 Code reconnects saved environments by itself and gives up on SSH within 10 to 60 seconds, so waking on connect would wake every saved sandbox and still time out.
- A link from the Mac reaches the sandbox through the desktop's SSH port forward, so agents on the Mac can start new work there only while the desktop runs.
- The phone reaches a sandbox only through Remote Control.
