#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.13"
# dependencies = []
# ///
"""Create the storage account the Terraform state lives in, and grant access to it. Safe to rerun.

    deploy/scripts/bootstrap-tfstate.py [--deployer <principal-id>] [--dry-run]

Terraform cannot create the place its own state lives, so this runs once before the first `terraform init`. The
account sits in its own resource group, apart from the foundation. The names below are the literals in
deploy/envs/prod/backend.tf, which cannot interpolate, so change both together.

The signed-in user always gets Storage Blob Data Contributor. --deployer grants it to the GitHub Actions identity too,
whose principal ID is the Terraform output github_actions_principal_id.

Needs the Azure CLI signed in to an account that owns the subscription. Every call names the subscription, so the
CLI's default account is never used.
"""

import argparse
import base64
import json
import subprocess
import sys

SUBSCRIPTION = "d349301a-5b5b-4fa0-b6a9-9a30d80b7047"
RESOURCE_GROUP = "rg-skybox-tfstate"
STORAGE_ACCOUNT = "stskyboxamaretfstate"
CONTAINER = "tfstate"
LOCATION = "germanywestcentral"
TAG = "skybox:component=tfstate"
RETENTION_DAYS = "30"
ROLE = "Storage Blob Data Contributor"


def log(message: str) -> None:
    print(message, file=sys.stderr)


def az(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    proc = subprocess.run(["az", *args, "--subscription", SUBSCRIPTION], capture_output=True, text=True)
    if check and proc.returncode != 0:
        sys.exit(f"az {' '.join(args)} failed:\n{proc.stderr.strip()}")
    return proc


def exists(*args: str) -> bool:
    return az(*args, check=False).returncode == 0


def signed_in_object_id() -> str:
    """The oid claim of the subscription's own token. `az ad signed-in-user show` answers from the CLI's default
    tenant, which can be a different one."""
    token = az("account", "get-access-token", "--query", "accessToken", "-o", "tsv").stdout.strip()
    payload = token.split(".")[1]
    claims = json.loads(base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4)))
    return claims["oid"]


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--deployer", action="append", default=[], metavar="PRINCIPAL_ID")
    parser.add_argument("--dry-run", action="store_true", help="print what would change, change nothing")
    args = parser.parse_args()

    def run(*cmd: str) -> None:
        if args.dry_run:
            log(f"    [dry-run] az {' '.join(cmd)}")
        else:
            az(*cmd)

    log(f"==> {STORAGE_ACCOUNT} in {RESOURCE_GROUP} ({LOCATION})")
    made = "would be created" if args.dry_run else "created"

    if exists("group", "show", "-n", RESOURCE_GROUP):
        log(f"    resource group {RESOURCE_GROUP} exists")
    else:
        run("group", "create", "-n", RESOURCE_GROUP, "-l", LOCATION, "--tags", TAG, "-o", "none")
        log(f"    resource group {RESOURCE_GROUP} {made}")

    # Shared keys are off, so the backend authenticates and takes its lock lease with Entra.
    if exists("storage", "account", "show", "-n", STORAGE_ACCOUNT, "-g", RESOURCE_GROUP):
        log(f"    storage account {STORAGE_ACCOUNT} exists")
    else:
        run(
            "storage", "account", "create",
            "-n", STORAGE_ACCOUNT, "-g", RESOURCE_GROUP, "-l", LOCATION,
            "--sku", "Standard_LRS",
            "--kind", "StorageV2",
            "--min-tls-version", "TLS1_2",
            "--https-only", "true",
            "--allow-blob-public-access", "false",
            "--allow-shared-key-access", "false",
            "--tags", TAG,
            "-o", "none",
        )
        log(f"    storage account {STORAGE_ACCOUNT} {made}")

    # The state file is the only record of what exists, so versioning and soft delete keep a bad write recoverable.
    run(
        "storage", "account", "blob-service-properties", "update",
        "--account-name", STORAGE_ACCOUNT, "--resource-group", RESOURCE_GROUP,
        "--enable-versioning", "true",
        "--enable-delete-retention", "true",
        "--delete-retention-days", RETENTION_DAYS,
        "--enable-container-delete-retention", "true",
        "--container-delete-retention-days", RETENTION_DAYS,
        "-o", "none",
    )
    log(f"    versioning on, {RETENTION_DAYS}-day soft delete")

    if args.dry_run:
        log("    [dry-run] container and role assignments skipped")
        return

    # Owner does not include data-plane access, so the signed-in user needs the role before it can create the container.
    scope = az("storage", "account", "show", "-n", STORAGE_ACCOUNT, "-g", RESOURCE_GROUP, "--query", "id", "-o", "tsv")
    grants = [(signed_in_object_id(), "User")] + [(principal, "ServicePrincipal") for principal in args.deployer]
    for principal_id, principal_type in grants:
        az(
            "role", "assignment", "create",
            "--assignee-object-id", principal_id,
            "--assignee-principal-type", principal_type,
            "--role", ROLE,
            "--scope", scope.stdout.strip(),
            "-o", "none",
        )
        log(f"    {principal_type} {principal_id} holds {ROLE}")

    container = ("--name", CONTAINER, "--account-name", STORAGE_ACCOUNT, "--auth-mode", "login")
    if exists("storage", "container", "show", *container):
        log(f"    container {CONTAINER} exists")
    elif not exists("storage", "container", "create", *container, "-o", "none"):
        sys.exit("Creating the container failed. A new role assignment takes a minute or two to apply, so rerun this.")
    else:
        log(f"    container {CONTAINER} created")


if __name__ == "__main__":
    main()
