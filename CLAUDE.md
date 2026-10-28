# skybox

## Changing code

When a requirement changes, **rewrite** every unit the change touches (the function, component or module) as a fresh first draft against the current requirements, then replace the old version with it. Read the whole unit, its callers and its tests first. A patch layered onto code written for an old requirement leaves branches, names and comments that answer questions nobody asks anymore. The next reader cannot tell which parts are still live, and the next patch builds on the dead ones.

- The rewrite reads as if the unit had been written today: names, structure and control flow follow the current requirement and carry no trace of earlier ones.
- Comments are rewritten with the code. Each one states what is true now, so a comment that mentions the old behaviour, the change itself, or "now" and "no longer" is rewritten or deleted.
- Keep the unit's contract (command, flag, JSON output) unless the requirement changes it. If it changes, update every caller and test in the same change.
- A defect in code whose requirements still hold gets a targeted fix. A rewrite is for a changed requirement.

## Language

Code, docs, tickets and commits are English and use the terms in `GLOSSARY.md`. Read it before naming a domain concept or describing a command, and add the term there when you introduce a new one.

## Decisions

Accepted ADRs live in `docs/adr/`. Draft a proposed ADR in `docs/drafts/`, and move it to `docs/adr/` once it is accepted.

## Tickets

A ticket is a GitHub issue, and it is where the work is specified. Create one only when a person asks for that ticket. Until then, draft its title and body in chat or a PR comment.

Write each ticket from the form in `.github/ISSUE_TEMPLATE/` that matches its kind (`feature.yml`, `bug.yml`, `tech-debt.yml`): each field's label becomes a `###` heading, and its description says what goes under it. Omit an optional section that has nothing to say. The reader is a teammate who opens the ticket cold, so the body describes what the user gets, in plain prose, and leaves the work log to the PR.

- Write the title as the Conventional Commit subject the change will land under, naming the outcome: `feat(cli): wake a sleeping sandbox with its disk intact`. It becomes the PR title, so it reads on its own in `git log`.
- Context names who has the problem and what they cannot do today, then links the ADR in `docs/adr/` and any ticket it depends on.
- Each acceptance criterion is one behaviour: what someone does, and what they then see. "The user runs `skybox up api-fix` and sees `api-fix` in T3 Code's environment list within five minutes." Give each failure case its own item, with the exit code or error it answers, and state a capability that must be absent as an item too. End the list with "Each behaviour above has a test, and the test suite of every component it touches passes."
- Approach is the design in a few sentences: where the code lives, the one structural idea, and its reason. The step-by-step plan, file lists and code belong to whoever picks the ticket up, who works them out against the code as it is then.
- Each open question is a real fork: name the options and what each one costs.
- In tech debt, Current state opens each problem with a bold one-line verdict, then the concrete failure it causes.

## Commits

- Commit subjects follow Conventional Commits. lefthook's `commit-msg` hook checks local commits and the `pr-title` workflow checks PR titles, both with `commitlint.config.ts`. That file holds the allowed scopes, so add a scope there before you first use it.
- `master` accepts only squash merges of pull requests. The PR title becomes the commit subject, so write the title as the commit.
- Write every PR body from `.github/PULL_REQUEST_TEMPLATE.md`: keep each heading, fill every section by its guidance comment, then delete the comments. `gh pr create --body` skips the template that GitHub's web form applies, so a body written from memory drops it, and the body becomes the squash commit's message, so a comment left in it lands in `git log`. Add `Closes #<number>` only for an issue that already exists.
- Tick every checkbox in the PR body before the merge. A check you cannot run until after the merge goes in a PR comment instead: the squash commit freezes the body in `git log`, so a `- [ ]` left there stays unticked forever, while a comment can still be edited.
- Keep your PR comments current. When a check passes or its result changes, edit the comment that holds it (`gh pr comment --edit-last`, or `gh api --method PATCH repos/{owner}/{repo}/issues/comments/<id>`) rather than posting a new one under it, so the reader does not meet the stale state first.
- A change that bumps the version in `package.json` adds a section for that version to `CHANGELOG.md` in the same commit, because nothing writes the changelog for you and a version without its entry leaves the release undocumented.
- `scripts/setup-github.py` defines the repository's settings, labels and ruleset. Change a setting in the script and rerun it, so the script keeps matching the live repository.

## Agent skills

### Issue tracker

Issues live in GitHub Issues on `richardamare/skybox`. See `docs/agents/issue-tracker.md`.

### Triage labels

The five default triage labels, plus `feature`, `bug` and `tech-debt` as categories. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.
