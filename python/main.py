"""CLI entry point — ``python main.py``.

Parses flags, authenticates, resolves Fabric resources, optionally configures
VNet, creates the mirror, starts replication, and optionally restores network
settings.
"""

from __future__ import annotations

import argparse
import sys

from azure.mgmt.cosmosdb import CosmosDBManagementClient

from auth import build_credential, acquire_token
from fabric_client import (
    derive_account_name,
    lookup_workspace,
    lookup_connection,
    lookup_folder,
)
from mirroring import create_mirrored_database, start_mirroring
from vnet import run_vnet_setup, restore_network_settings


def main() -> None:
    parser = argparse.ArgumentParser(
        prog="cosmos-mirror",
        description=(
            "Automate Microsoft Fabric Mirrored Database creation for Azure Cosmos DB. "
            "Creates the mirror artifact, starts replication, and optionally configures "
            "Cosmos DB networking (RBAC, IP firewall, Network ACL bypass)."
        ),
    )

    # ── Authentication flags ──────────────────────────────────────────
    auth = parser.add_argument_group("Authentication")
    auth.add_argument("--tenant", default="",
                      help="Entra tenant ID (optional; narrows credential selection)")
    auth.add_argument("--client", default="",
                      help="App registration client ID (optional; used with --interactive)")
    auth.add_argument("--managed-identity-client-id", default="",
                      help="User-assigned managed identity client ID")
    auth.add_argument("--interactive", action="store_true",
                      help="Use interactive browser sign-in instead of DefaultAzureCredential")

    # ── Fabric workspace ──────────────────────────────────────────────
    fabric = parser.add_argument_group("Fabric workspace")
    fabric.add_argument("--workspace", required=True,
                        help="Fabric workspace name")

    # ── Cosmos DB source ──────────────────────────────────────────────
    cosmos = parser.add_argument_group("Cosmos DB source")
    cosmos.add_argument("--cosmos-endpoint", required=True,
                        help="Cosmos DB account endpoint")
    cosmos.add_argument("--database", required=True,
                        help="Cosmos DB database name")

    # ── Connection ────────────────────────────────────────────────────
    conn = parser.add_argument_group("Fabric connection")
    conn.add_argument("--connection", required=True,
                      help="Name of a pre-existing Fabric connection (create via Fabric portal)")

    # ── Mirrored database ─────────────────────────────────────────────
    mirror = parser.add_argument_group("Mirrored database")
    mirror.add_argument("--mirror-name", required=True,
                        help="Display name for the mirrored database artifact")
    mirror.add_argument("--folder", default="",
                        help="Fabric workspace folder name (optional)")
    mirror.add_argument("--containers", default="",
                        help="Comma-separated list of container names to mirror (optional)")
    mirror.add_argument("--auto-discover", action="store_true",
                        help="Automatically mirror new containers (default: false)")

    # ── VNet configuration ────────────────────────────────────────────
    vnet = parser.add_argument_group("VNet configuration (--configure-vnet)")
    vnet.add_argument("--configure-vnet", action="store_true",
                      help="Configure Cosmos DB networking for Fabric mirroring")
    vnet.add_argument("--subscription", default="",
                      help="Azure subscription ID (required with --configure-vnet)")
    vnet.add_argument("--resource-group", default="",
                      help="Resource group containing the Cosmos DB account")
    vnet.add_argument("--principal-id", default="",
                      help="Service principal/user object ID for RBAC (auto-detected if omitted)")
    vnet.add_argument("--skip-rbac", action="store_true",
                      help="Skip RBAC setup when using --configure-vnet")
    vnet.add_argument("--skip-firewall", action="store_true",
                      help="Skip IP firewall configuration")
    vnet.add_argument("--skip-network-acl", action="store_true",
                      help="Skip Network ACL bypass configuration")
    vnet.add_argument("--no-restore", action="store_true",
                      help="Don't restore original network settings after mirror creation")

    args = parser.parse_args()

    # ── Validate VNet flags ───────────────────────────────────────────
    resolved_account_name = ""
    if args.configure_vnet:
        if not args.subscription or not args.resource_group:
            parser.error("--subscription and --resource-group are required with --configure-vnet")
        resolved_account_name = derive_account_name(args.cosmos_endpoint)
        if not resolved_account_name:
            print("ERROR: Could not derive account name from --cosmos-endpoint", file=sys.stderr)
            sys.exit(1)
        print(f"Derived account name: {resolved_account_name}")

    # Parse optional container list
    container_list: list[str] = []
    if args.containers:
        container_list = [c.strip() for c in args.containers.split(",") if c.strip()]

    # ── Authenticate ──────────────────────────────────────────────────
    credential = build_credential(
        tenant=args.tenant or None,
        client_id=args.client or None,
        managed_identity_client_id=args.managed_identity_client_id or None,
        interactive=args.interactive,
    )
    token = acquire_token(credential)

    # 1. Resolve workspace by name
    workspace_id = lookup_workspace(token, args.workspace)
    print(f"Workspace ID: {workspace_id}")

    # 2. Configure VNet networking (Steps 1-3) — optional
    initial_state = None
    cosmos_client = None
    if args.configure_vnet:
        cosmos_client = CosmosDBManagementClient(credential, args.subscription)
        initial_state = run_vnet_setup(
            credential=credential,
            cosmos_client=cosmos_client,
            subscription_id=args.subscription,
            rg=args.resource_group,
            account_name=resolved_account_name,
            principal_id_override=args.principal_id,
            workspace_id=workspace_id,
            tenant_flag=args.tenant,
            skip_rbac=args.skip_rbac,
            skip_firewall=args.skip_firewall,
            skip_network_acl=args.skip_network_acl,
        )
        print("\n── Step 4: Create Fabric Mirror ──────────────────────────────────")

    # 3. Look up the pre-existing connection by display name
    connection_id = lookup_connection(token, args.connection)
    print(f"Using connection: {connection_id}")

    # 4. Optionally resolve folder by name
    folder_id = ""
    if args.folder:
        folder_id = lookup_folder(token, workspace_id, args.folder)
        print(f"Folder ID: {folder_id}")

    # 5. Create the mirrored database item
    mirror_id = create_mirrored_database(
        token=token,
        workspace_id=workspace_id,
        connection_id=connection_id,
        database=args.database,
        display_name=args.mirror_name,
        folder_id=folder_id or None,
        auto_discover=args.auto_discover,
        container_list=container_list,
    )
    print(f"Mirrored Database ID: {mirror_id}")

    # 6. Start mirroring
    start_mirroring(token, workspace_id, mirror_id)
    print("Mirroring started successfully")

    # 7. Restore network settings (Step 5) — optional
    if args.configure_vnet and not args.no_restore and initial_state is not None:
        if cosmos_client is None:
            cosmos_client = CosmosDBManagementClient(credential, args.subscription)
        restore_network_settings(
            cosmos_client, args.resource_group, resolved_account_name, initial_state
        )


if __name__ == "__main__":
    main()
