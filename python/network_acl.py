"""Network ACL bypass configuration (Step 3).

Enables the ``EnableFabricNetworkAclBypass`` capability and registers the
Fabric workspace resource ID in the bypass allowlist.
"""

from __future__ import annotations

import sys

from azure.mgmt.cosmosdb.models import (
    Capability,
    DatabaseAccountUpdateParameters,
)

FABRIC_NETWORK_ACL_BYPASS_CAPABILITY = "EnableFabricNetworkAclBypass"


def configure_network_acl_bypass(cosmos_client, rg: str, account_name: str,
                                 workspace_id: str, tenant_id: str,
                                 state) -> None:
    """Enable Fabric Network ACL Bypass and register the workspace.

    Args:
        cosmos_client: ``CosmosDBManagementClient`` instance.
        rg: Resource group name.
        account_name: Cosmos DB account name.
        workspace_id: Fabric workspace ID.
        tenant_id: Entra tenant ID (GUID).
        state: Captured ``AccountState``.
    """
    print("\n── Step 3: Network ACL Bypass Configuration ─────────────────────")

    # 3a. Enable the EnableFabricNetworkAclBypass capability
    _enable_fabric_capability(cosmos_client, rg, account_name, state)

    # 3b. Build the Fabric workspace resource ID
    fabric_resource_id = (
        f"/tenants/{tenant_id}"
        f"/subscriptions/00000000-0000-0000-0000-000000000000"
        f"/resourceGroups/Fabric"
        f"/providers/Microsoft.Fabric/workspaces/{workspace_id}"
    )
    print(f"Fabric resource ID: {fabric_resource_id}")

    # 3c. Merge with existing bypass resource IDs
    bypass_set: set[str] = set()
    for rid in state.network_acl_bypass_resource_ids:
        if rid:
            bypass_set.add(rid)
    bypass_set.add(fabric_resource_id)

    merged_bypass_ids = list(bypass_set)
    print(f"Bypass resource IDs: {len(merged_bypass_ids)} "
          f"(was {len(state.network_acl_bypass_resource_ids)})")

    # 3d. Update the account
    params = DatabaseAccountUpdateParameters(
        network_acl_bypass="AzureServices",
        network_acl_bypass_resource_ids=merged_bypass_ids,
    )

    print("Updating Network ACL Bypass (this may take several minutes)...")
    poller = cosmos_client.database_accounts.begin_update(rg, account_name, params)
    poller.result()
    print("Network ACL Bypass configured successfully.")


def _enable_fabric_capability(cosmos_client, rg: str, account_name: str,
                              state) -> None:
    """Add the ``EnableFabricNetworkAclBypass`` capability if not already present."""

    # Already enabled?
    for cap in state.capabilities:
        if cap.name and cap.name.lower() == FABRIC_NETWORK_ACL_BYPASS_CAPABILITY.lower():
            print(f"Capability '{FABRIC_NETWORK_ACL_BYPASS_CAPABILITY}' already enabled.")
            return

    # Merge existing capabilities + new one
    caps = list(state.capabilities) + [
        Capability(name=FABRIC_NETWORK_ACL_BYPASS_CAPABILITY)
    ]

    params = DatabaseAccountUpdateParameters(capabilities=caps)

    print(f"Enabling capability '{FABRIC_NETWORK_ACL_BYPASS_CAPABILITY}' "
          f"(this may take several minutes)...")
    poller = cosmos_client.database_accounts.begin_update(rg, account_name, params)
    poller.result()
    print(f"Capability '{FABRIC_NETWORK_ACL_BYPASS_CAPABILITY}' enabled.")
