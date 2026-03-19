"""VNet orchestration — account state capture, restore, and setup flow.

Orchestrates Steps 1–3 (RBAC, IP Firewall, Network ACL Bypass) and
captures/restores account state for Step 5.
"""

from __future__ import annotations

import re
import sys
from dataclasses import dataclass, field
from typing import Any

from azure.mgmt.cosmosdb import CosmosDBManagementClient
from azure.mgmt.cosmosdb.models import (
    DatabaseAccountUpdateParameters,
)

from auth import get_principal_object_id, get_tenant_id
from fabric_client import normalize_location
from network_acl import configure_network_acl_bypass
from rbac import setup_rbac
from service_tags import configure_ip_firewall


# ---------------------------------------------------------------------------
# Account state capture / restore
# ---------------------------------------------------------------------------

@dataclass
class AccountState:
    """Snapshot of a Cosmos DB account's networking configuration."""
    public_network_access: str | None = None
    ip_rules: list = field(default_factory=list)
    network_acl_bypass: str | None = None
    network_acl_bypass_resource_ids: list = field(default_factory=list)
    location: str = ""
    capabilities: list = field(default_factory=list)


def capture_account_state(cosmos_client: CosmosDBManagementClient,
                          rg: str, account_name: str) -> AccountState:
    """Read the current Cosmos DB account networking configuration."""
    acct = cosmos_client.database_accounts.get(rg, account_name)

    state = AccountState()
    if acct.location:
        state.location = acct.location
    if acct.public_network_access:
        state.public_network_access = acct.public_network_access
    state.ip_rules = list(acct.ip_rules or [])
    if acct.network_acl_bypass:
        state.network_acl_bypass = acct.network_acl_bypass
    state.network_acl_bypass_resource_ids = list(acct.network_acl_bypass_resource_ids or [])
    state.capabilities = list(acct.capabilities or [])
    return state


def restore_network_settings(cosmos_client: CosmosDBManagementClient,
                             rg: str, account_name: str,
                             state: AccountState) -> None:
    """Restore the Cosmos DB account's public-network access and IP rules.

    Network ACL bypass is intentionally NOT restored; Fabric needs it for
    ongoing access through private endpoints.
    """
    print("\n── Step 5: Restoring network settings ───────────────────────────")

    params = DatabaseAccountUpdateParameters(
        public_network_access=state.public_network_access,
        ip_rules=state.ip_rules,
    )

    print("Restoring PublicNetworkAccess and IP rules (this may take several minutes)...")
    poller = cosmos_client.database_accounts.begin_update(rg, account_name, params)
    poller.result()
    print("Network settings restored to initial state.")


# ---------------------------------------------------------------------------
# VNet setup orchestration
# ---------------------------------------------------------------------------

def run_vnet_setup(credential,
                   cosmos_client: CosmosDBManagementClient,
                   subscription_id: str,
                   rg: str,
                   account_name: str,
                   principal_id_override: str,
                   workspace_id: str,
                   tenant_flag: str,
                   skip_rbac: bool,
                   skip_firewall: bool,
                   skip_network_acl: bool) -> AccountState:
    """Orchestrate networking setup Steps 1–3.

    Returns the captured initial state so it can be restored in Step 5.
    """

    # ── Phase 0: Capture initial state ─────────────────────────────────
    print("\n── Phase 0: Capturing initial account state ─────────────────────")
    state = capture_account_state(cosmos_client, rg, account_name)

    location = normalize_location(state.location)
    print(f"Account: {account_name}")
    print(f"Location: {location}")
    if state.public_network_access:
        print(f"PublicNetworkAccess: {state.public_network_access}")
    print(f"IP rules: {len(state.ip_rules)}")
    print(f"Capabilities: {len(state.capabilities)}")

    # ── Determine principal ID ─────────────────────────────────────────
    principal_id = principal_id_override
    if not principal_id:
        try:
            principal_id = get_principal_object_id(credential)
        except Exception as ex:
            print(f"ERROR: Failed to determine principal ID: {ex}\n"
                  f"Provide --principal-id explicitly.", file=sys.stderr)
            sys.exit(1)
    print(f"Principal ID: {principal_id}")

    # ── Determine tenant ID ────────────────────────────────────────────
    tenant_id = tenant_flag
    if not tenant_id or not _is_guid(tenant_id):
        try:
            tenant_id = get_tenant_id(credential)
        except Exception as ex:
            print(f"ERROR: Failed to determine tenant ID: {ex}\n"
                  f"Provide --tenant with a GUID.", file=sys.stderr)
            sys.exit(1)
    print(f"Tenant ID: {tenant_id}")

    # ── Step 1: RBAC ──────────────────────────────────────────────────
    if not skip_rbac:
        account_scope = (
            f"/subscriptions/{subscription_id}"
            f"/resourceGroups/{rg}"
            f"/providers/Microsoft.DocumentDB/databaseAccounts/{account_name}"
        )
        setup_rbac(cosmos_client, rg, account_name, account_scope, principal_id)
    else:
        print("\n── Step 1: RBAC — skipped (--skip-rbac) ─────────────────────────")

    # ── Step 2: IP Firewall ───────────────────────────────────────────
    if not skip_firewall:
        configure_ip_firewall(
            credential, cosmos_client, subscription_id,
            rg, account_name, location, state,
        )
    else:
        print("\n── Step 2: IP Firewall — skipped (--skip-firewall) ──────────────")

    # ── Step 3: Network ACL Bypass ────────────────────────────────────
    if not skip_network_acl:
        configure_network_acl_bypass(cosmos_client, rg, account_name, workspace_id, tenant_id, state)
    else:
        print("\n── Step 3: Network ACL Bypass — skipped (--skip-network-acl) ────")

    return state


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

_GUID_RE = re.compile(
    r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$",
    re.IGNORECASE,
)


def _is_guid(s: str) -> bool:
    """Return True if *s* looks like a UUID / GUID."""
    return bool(_GUID_RE.match(s.strip()))
