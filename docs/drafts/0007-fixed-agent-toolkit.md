# A sandbox gets a fixed agent toolkit, with agent-config copied from the Mac

An agent on a sandbox needs the user's skills, hooks and global rules to work the way agents on the Mac do. `skybox sync <name>` gives a running sandbox the agent toolkit: the plugins `agent-config@agent-config`, `mattpocock-skills@mattpocock` and `pstack@pstack-claude`, and agent-config's `instructions/AGENTS.md` as `~/.claude/CLAUDE.md`. `skybox up` and `skybox wake` run the same sync as their last step. The plugin list is fixed in the CLI, in `internal/toolkit`, because it is what a sandbox's agents need, and it changes with skybox rather than with the Mac.

`richardamare/agent-config` is private, and a sandbox holds no GitHub credentials. The CLI therefore streams the Mac's clone at `~/.claude/plugins/marketplaces/agent-config` as a tar archive over the sandbox's ssh alias into `~/.skybox/marketplaces/agent-config`, and adds that directory as a marketplace. The copy includes `.git`, because Claude Code takes a plugin's version from the commit when its manifest names none. The other two marketplaces are public, and the sandbox adds them from GitHub itself. Every change on the sandbox goes through Claude Code's own CLI (`claude plugin marketplace add|update`, `claude plugin install|update|uninstall`), so skybox never writes Claude Code's state files.

## Considered Options

- **The Mac's devbox toolkit manifest as the list.** Rejected: it describes the Mac's whole toolkit, most of which belongs on the Mac, and every change to the Mac's tools would change what each sandbox runs.
- **A GitHub credential on the sandbox, which clones agent-config itself.** Rejected: every sandbox carries the same managed identity (ADR 0006), so a credential in Key Vault would be readable by every sandbox, and a sandbox would get what GitHub holds rather than what the Mac runs.
- **The toolkit in cloud-init.** Rejected: cloud-init runs on the first boot alone, so a sandbox would keep the toolkit it was born with, and the private repository would still need a credential.

## Consequences

- A sync replaces the sandbox's copy of agent-config whole, overwrites `~/.claude/CLAUDE.md`, and uninstalls every user-scope plugin outside the list. A plugin or rule an agent adds on a sandbox lasts until the next `sync`, `up` or `wake`.
- `skybox sync` reaches a sandbox the way `skybox proxy` does: it fails at once, with proxy's message, for a sleeping or missing sandbox, and never wakes or creates one (ADR 0003).
- Without an agent-config clone on the Mac, `up` and `wake` skip the toolkit and say so, and `sync` fails naming the directory.
- Adding or removing a plugin in the toolkit is a skybox release. A plugin update reaches the sandbox's agents at their next Claude Code session.
