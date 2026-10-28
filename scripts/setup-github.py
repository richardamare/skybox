#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.13"
# dependencies = []
# ///
"""Create the public GitHub repository if it is missing and apply its settings, labels, security features, admin
access and the master ruleset. Safe to rerun: every step converges on the state below.

The person running the script ends up as the only repository admin; every other admin collaborator or team drops to
maintain.

Usage: scripts/setup-github.py <owner/name>
"""

import json
import subprocess
import sys

if len(sys.argv) != 2:
    sys.exit(__doc__)
REPO = sys.argv[1]
RULESET_NAME = "master"

SETTINGS = {
    "private": False,
    "has_wiki": False,
    "has_projects": False,
    "has_discussions": False,
    "allow_squash_merge": True,
    "allow_merge_commit": False,
    "allow_rebase_merge": False,
    "squash_merge_commit_title": "PR_TITLE",
    "squash_merge_commit_message": "PR_BODY",
    "allow_auto_merge": True,
    "delete_branch_on_merge": True,
    "allow_update_branch": True,
    "web_commit_signoff_required": False,
}

LABELS = {
    "feature": ("0e8a16", "A new capability"),
    "bug": ("d73a4a", "Something is broken"),
    "tech-debt": ("c2e0c6", "Refactor, cleanup or tooling"),
    "dependencies": ("0366d6", "Dependency update"),
    "blocked": ("e99695", "Waiting on a decision or an outside dependency"),
}

# Actor 5 is the repository admin role, which apply_admins leaves to the person running this script.
RULESET = {
    "name": RULESET_NAME,
    "target": "branch",
    "enforcement": "active",
    "conditions": {"ref_name": {"include": ["~DEFAULT_BRANCH"], "exclude": []}},
    "bypass_actors": [{"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always"}],
    "rules": [
        {"type": "deletion"},
        {"type": "non_fast_forward"},
        {"type": "required_linear_history"},
        {
            "type": "pull_request",
            "parameters": {
                "required_approving_review_count": 0,
                "dismiss_stale_reviews_on_push": False,
                "require_code_owner_review": False,
                "require_last_push_approval": False,
                "required_review_thread_resolution": False,
                "allowed_merge_methods": ["squash"],
            },
        },
        {
            "type": "required_status_checks",
            "parameters": {
                "strict_required_status_checks_policy": False,
                "required_status_checks": [{"context": "ci-ok"}, {"context": "commitlint"}],
            },
        },
    ],
}


def run(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(args, check=check, capture_output=True, text=True)


def api(method: str, path: str, body: object | None = None, check: bool = True) -> subprocess.CompletedProcess[str]:
    args = ["gh", "api", "--method", method, f"repos/{REPO}{path}"]
    if body is None:
        return run(*args, check=check)
    return subprocess.run([*args, "--input", "-"], input=json.dumps(body), check=check, capture_output=True, text=True)


def step(title: str) -> None:
    print(f"==> {title}")


def ensure_repository() -> None:
    if run("gh", "repo", "view", REPO, check=False).returncode != 0:
        step(f"Creating public repository {REPO}")
        run("gh", "repo", "create", REPO, "--public")
    if run("git", "remote", "get-url", "origin", check=False).returncode != 0:
        run("git", "remote", "add", "origin", f"https://github.com/{REPO}.git")


def apply_settings() -> None:
    step("Repository settings")
    api("PATCH", "", SETTINGS)

    step("Actions permissions")
    api("PUT", "/actions/permissions/workflow", {"default_workflow_permissions": "read", "can_approve_pull_request_reviews": False})


def apply_security() -> None:
    step("Security features")
    api("PUT", "/vulnerability-alerts")
    api("PUT", "/automated-security-fixes")
    scanning = {
        "security_and_analysis": {
            "secret_scanning": {"status": "enabled"},
            "secret_scanning_push_protection": {"status": "enabled"},
        }
    }
    if api("PATCH", "", scanning, check=False).returncode != 0:
        print("    secret scanning is unavailable on this plan; skipped")


def apply_labels() -> None:
    step("Labels")
    existing = json.loads(run("gh", "label", "list", "--repo", REPO, "--limit", "200", "--json", "name").stdout)
    for label in existing:
        if label["name"] not in LABELS:
            run("gh", "label", "delete", label["name"], "--repo", REPO, "--yes")
    for name, (color, description) in LABELS.items():
        run("gh", "label", "create", name, "--repo", REPO, "--color", color, "--description", description, "--force")


def apply_admins() -> None:
    me = run("gh", "api", "user", "--jq", ".login").stdout.strip()
    step(f"Admin access for {me} only")
    if json.loads(api("GET", f"/collaborators/{me}/permission").stdout)["permission"] != "admin":
        sys.exit(f"{me} is not an admin of {REPO}, so cannot take admin away from anyone else")

    admins = run(
        "gh", "api", "--paginate", f"repos/{REPO}/collaborators?affiliation=direct&per_page=100",
        "--jq", ".[] | select(.permissions.admin) | .login",
    ).stdout.split()
    for login in admins:
        if login != me:
            api("PUT", f"/collaborators/{login}", {"permission": "maintain"})
            print(f"    {login}: admin -> maintain")

    # A repository owned by a user has no teams, and this request fails there.
    teams = run(
        "gh", "api", "--paginate", f"repos/{REPO}/teams?per_page=100",
        "--jq", '.[] | select(.permission == "admin") | .slug',
        check=False,
    ).stdout.split()
    org = REPO.split("/")[0]
    for slug in teams:
        run("gh", "api", "--method", "PUT", f"orgs/{org}/teams/{slug}/repos/{REPO}", "-f", "permission=maintain")
        print(f"    team {slug}: admin -> maintain")

    print("    organization owners keep admin on every repository; GitHub offers no per-repository override")


def apply_ruleset() -> None:
    step("Ruleset on master")
    rulesets = json.loads(api("GET", "/rulesets").stdout)
    existing = next((r["id"] for r in rulesets if r["name"] == RULESET_NAME), None)
    if existing is None:
        api("POST", "/rulesets", RULESET)
    else:
        api("PUT", f"/rulesets/{existing}", RULESET)


def main() -> None:
    try:
        ensure_repository()
        apply_settings()
        apply_security()
        apply_labels()
        apply_admins()
        apply_ruleset()
    except subprocess.CalledProcessError as error:
        sys.exit(f"{' '.join(error.cmd)} failed:\n{error.stderr}")
    print(f"==> Done: https://github.com/{REPO}")


if __name__ == "__main__":
    main()
