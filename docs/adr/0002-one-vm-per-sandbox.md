# A sandbox is one Azure VM that sleeps by deallocating

Each sandbox is its own Azure VM with a persistent managed disk. Sleep deallocates the VM, so it costs only its disk until it wakes with the same files. Containers were considered and rejected, because a VM keeps a persistent disk and sleeps by deallocating, so an idle sandbox costs only its storage.

## Consequences

- One sandbox holds several agents. Each agent is a T3 Code thread in its own git worktree, so an extra agent never needs a second sandbox. A sandbox usually serves one project for its whole life.
- Agents in one sandbox share the agent account and the disk, so a sandbox is for one project's trusted work, and their dev servers must not collide on ports.
- The sandbox counts as idle only when no thread runs in it.
- The subscription's 10 regional vCPUs in germanywestcentral bound how many sandboxes run at once.
