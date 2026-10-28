# Linked environments reach a sandbox over Tailscale, the desktop still over SSH

A link under Linked environments needs an address the Mac reaches without the T3 Code desktop. The desktop's SSH port forward (ADR 0003) changes on every connection and exists only while the desktop runs, so every sandbox joins the user's tailnet instead, and its T3 Code server publishes itself there with Tailscale Serve (`T3CODE_TAILSCALE_SERVE=true`). T3 Code then reports the sandbox's `https://<name>.<tailnet>.ts.net` address, and the link card fills it in. The desktop keeps reaching the sandbox over SSH through Cloudflare Access, as ADR 0003 decided.

`skybox up` reads the OAuth client from the 1Password item `Tailscale OAuth client` (ADR 0004) and mints one auth key per sandbox: tagged `tag:skybox`, pre-approved, single use, not ephemeral, valid for 30 minutes. The OAuth client's secret stays in the CLI's process. `skybox down` deletes the sandbox's device through the API.

## Considered Options

- **The auth key in Key Vault**, read on first boot with the managed identity like the Claude token. Rejected: every sandbox carries the same identity, so a per-sandbox secret would be readable by every other sandbox while it is live, and the CLI would need write access to Key Vault, plus cleanup of each secret through soft delete. **The auth key in custom data** was chosen. Only the one VM it is created with sees it, the Azure API returns no custom data after creation, and it is not in instance metadata. cloud-init writes it to a file only root reads, and the join removes that file. A copy stays in cloud-init's own root-only state, where it is useless once spent or expired. The tunnel token reaches the sandbox the same way.
- **The tailnet policy in Terraform** with the `tailscale` provider. Rejected: it needs a second OAuth client with the `policy_file` scope for a policy that changes rarely. The policy is edited once in the admin console, and the `tests` below make the console refuse an edit that lets a sandbox reach anything.
- **Ephemeral keys.** Rejected: Tailscale removes an ephemeral device soon after it goes offline, and a sleeping sandbox is offline, so it would wake at a new address.

## One-time setup

In the admin console of the tailnet `richardamare.github`:

1. In DNS, turn on MagicDNS and HTTPS Certificates, which Tailscale Serve needs.
2. In Access controls, replace the default allow-all grant with this policy. Keep any other grant only if its `src` is neither `*` nor `tag:skybox`.

   ```jsonc
   {
     "tagOwners": {
       "tag:skybox": ["autogroup:admin"]
     },
     "grants": [
       // The user's own devices reach each other, as the default policy let them.
       { "src": ["autogroup:member"], "dst": ["autogroup:self"], "ip": ["*"] },
       // They reach a sandbox only where Tailscale Serve answers. No grant has a sandbox as its source, so a sandbox
       // reaches nothing on the tailnet.
       { "src": ["autogroup:member"], "dst": ["tag:skybox"], "ip": ["tcp:443"] }
     ],
     "tests": [
       { "src": "richardamare@github", "accept": ["tag:skybox:443"], "deny": ["tag:skybox:22"] },
       { "src": "tag:skybox", "deny": ["richardamare@github:443", "richardamare@github:22", "tag:skybox:443"] }
     ]
   }
   ```

3. In Trust credentials, the OAuth client of the 1Password item holds the scopes `auth_keys` and `devices:core`, both with the tag `tag:skybox`.

## Consequences

- A link works while the desktop is closed, and keeps its address across sleep and wake: tailscaled keeps the node on the disk, and a tagged device's key never expires.
- `skybox up` fails before it creates anything when the OAuth client cannot mint a key, and revokes the key when a later step fails. It fails, and leaves the sandbox for `skybox down`, when the sandbox has not joined the tailnet two minutes after its tunnel connects.
- A device of a sandbox's name that a failed `skybox down` leaves behind stays on the tailnet until the next `skybox up` of that name removes it, or until the user removes it in the admin console. The control plane does not hold the OAuth client and does not remove it.
- The Mac must be on the tailnet to use a link, and the phone still reaches a sandbox only through Remote Control.
