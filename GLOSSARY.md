# skybox

Provisions cloud machines where coding agents work, and links each one to T3 Code so threads can run there.

## Language

**Sandbox**:
One cloud machine skybox provisions for agent work, with its own disk, its agent account and a T3 Code server.
_Avoid_: Box, VM, instance (except for the cloud resource itself)

**Foundation**:
The shared resources every sandbox relies on, such as the network, the Key Vault and the identities, deployed once by Terraform from `deploy/`.
_Avoid_: Platform, infrastructure, base

**Provider**:
The cloud a sandbox runs in, together with the code that creates, sleeps, wakes and deletes it there.
_Avoid_: Backend, driver, cloud (for the code)

**Outbound access**:
The NAT gateway and its public IP that let sandboxes reach the internet. skybox creates them when a sandbox starts and deletes them once no sandbox runs.
_Avoid_: Egress, NAT (except for the Azure resource)

**Agent account**:
The unprivileged Linux user on a sandbox that runs the T3 Code server and every agent.
_Avoid_: Service user, runner

**Environment**:
A sandbox as T3 Code sees it: a T3 Code server the desktop reaches over SSH, listed under Settings → Environments and in Run on once added.
_Avoid_: Connection, remote, target

**Agent toolkit**:
The Claude Code plugins `agent-config@agent-config`, `mattpocock-skills@mattpocock` and `pstack@pstack-claude`, and the global `~/.claude/CLAUDE.md` from agent-config, which `skybox sync` gives a sandbox's agent account so its agents work as the Mac's do.
_Avoid_: Dotfiles, setup, plugins (for the whole set)

**Link**:
A sandbox added under Linked environments in T3 Code on the Mac, reached over the tailnet at `https://<name>.<tailnet>.ts.net`, so agents on the Mac start and delegate work there whether or not the desktop runs.
_Avoid_: Pairing, linked connection

**Tailnet**:
The user's Tailscale network. The Mac is on it, and each sandbox joins it tagged `tag:skybox`, reachable from the user's devices on 443 alone and reaching nothing itself.
_Avoid_: VPN, mesh, overlay

**Preview**:
A web app a sandbox serves on one of ports 3000 to 3009, which the user opens at `https://<port>-<name>-sbx.<zone>` after a Cloudflare Access sign-in.
_Avoid_: Port forward, public URL, share link

**Size profile**:
The named amount of compute a sandbox runs on: `light`, `standard` or `large`.
_Avoid_: Tier, plan, flavour, SKU (except for the provider's VM size)

**Sleep**:
Stopping a sandbox's compute while its disk stays, so it costs only storage until it wakes.
_Avoid_: Stop, pause, suspend, deallocate (except for the provider call)

**Wake**:
Starting a sleeping sandbox again with the disk it had.
_Avoid_: Start, resume

**Down**:
Deleting a sandbox and everything it holds.
_Avoid_: Destroy, remove, teardown

**Control plane**:
The scheduled job that puts idle sandboxes to sleep and deletes stale sandboxes and leftover resources.
_Avoid_: Reaper, janitor, scheduler

**Idle**:
A running sandbox where no T3 Code thread has run for an hour, counted from its latest run or its wake, whichever came later. Its CPU does not count.
_Avoid_: Inactive, unused

**Leftover**:
A resource with a skybox mark that outlived its sandbox: a disk, NIC, public IP or resource group with a `skybox:` tag, a `skybox-<name>` tunnel, or a DNS record whose comment names the sandbox. The control plane deletes it once it is older than an hour.
_Avoid_: Orphan, stale resource, garbage
