"""Cosmos SQL RBAC role creation and assignment (Step 1).

Creates the custom FabricMirroringRole and assigns it plus the built-in
Data Contributor role to the specified principal.
"""

from __future__ import annotations

import sys
import uuid

from azure.core.exceptions import HttpResponseError
from azure.mgmt.cosmosdb.models import (
    Permission,
    SqlRoleAssignmentCreateUpdateParameters,
    SqlRoleDefinitionCreateUpdateParameters,
    RoleDefinitionType,
)


# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

FABRIC_MIRRORING_ROLE_NAME = "FabricMirroringRole"
BUILTIN_DATA_CONTRIBUTOR_ID = "00000000-0000-0000-0000-000000000002"


# ---------------------------------------------------------------------------
# RBAC orchestration (Step 1)
# ---------------------------------------------------------------------------

def setup_rbac(cosmos_client, rg: str, account_name: str,
               account_scope: str, principal_id: str) -> None:
    """Create the custom FabricMirroringRole and assign roles to a principal.

    Assigns:
      1. The custom ``FabricMirroringRole`` (readMetadata + readAnalytics).
      2. The built-in Cosmos DB Data Contributor role.
    """
    print("\n── Step 1: RBAC Setup ────────────────────────────────────────────")

    # 1a. Create (or find) the custom FabricMirroringRole
    custom_role_id = _create_fabric_mirroring_role(
        cosmos_client, rg, account_name, account_scope
    )

    # 1b. Assign the custom role to the principal
    print("Assigning custom FabricMirroringRole...")
    _assign_cosmos_role(cosmos_client, rg, account_name, custom_role_id, principal_id, account_scope)

    # 1c. Assign the built-in Data Contributor role
    builtin_role_full_id = f"{account_scope}/sqlRoleDefinitions/{BUILTIN_DATA_CONTRIBUTOR_ID}"
    print("Assigning built-in Data Contributor role...")
    _assign_cosmos_role(cosmos_client, rg, account_name, builtin_role_full_id, principal_id, account_scope)

    print("RBAC setup complete.")


# ---------------------------------------------------------------------------
# Custom role definition
# ---------------------------------------------------------------------------

def _create_fabric_mirroring_role(cosmos_client, rg: str, account_name: str,
                                  account_scope: str) -> str:
    """Create the custom FabricMirroringRole or return its ID if it already exists."""

    # Check for an existing role with the same name (idempotent)
    for rd in cosmos_client.sql_resources.list_sql_role_definitions(rg, account_name):
        if rd.role_name == FABRIC_MIRRORING_ROLE_NAME:
            print(f"Custom role '{FABRIC_MIRRORING_ROLE_NAME}' already exists: {rd.id}")
            return rd.id

    # Create the custom role with a deterministic GUID
    role_def_id = str(uuid.uuid5(
        uuid.NAMESPACE_URL,
        f"{FABRIC_MIRRORING_ROLE_NAME}|{account_scope}",
    ))

    params = SqlRoleDefinitionCreateUpdateParameters(
        role_name=FABRIC_MIRRORING_ROLE_NAME,
        type=RoleDefinitionType.CUSTOM_ROLE,
        assignable_scopes=[account_scope],
        permissions=[
            Permission(data_actions=[
                "Microsoft.DocumentDB/databaseAccounts/readMetadata",
                "Microsoft.DocumentDB/databaseAccounts/readAnalytics",
            ])
        ],
    )

    print(f"Creating custom role '{FABRIC_MIRRORING_ROLE_NAME}'...")
    poller = cosmos_client.sql_resources.begin_create_update_sql_role_definition(
        role_def_id, rg, account_name, params
    )
    result = poller.result()
    print(f"Created custom role: {result.id}")
    return result.id


# ---------------------------------------------------------------------------
# Role assignment
# ---------------------------------------------------------------------------

def _assign_cosmos_role(cosmos_client, rg: str, account_name: str,
                        role_def_id: str, principal_id: str, scope: str) -> None:
    """Create a Cosmos SQL RBAC role assignment (idempotent).

    Uses a deterministic assignment GUID so reruns are safe.
    """
    assignment_id = str(uuid.uuid5(
        uuid.NAMESPACE_URL,
        f"{scope}|{role_def_id}|{principal_id}",
    ))

    params = SqlRoleAssignmentCreateUpdateParameters(
        role_definition_id=role_def_id,
        principal_id=principal_id,
        scope=scope,
    )

    try:
        poller = cosmos_client.sql_resources.begin_create_update_sql_role_assignment(
            assignment_id, rg, account_name, params
        )
        poller.result()
        print("  Role assignment created/updated.")
    except HttpResponseError as ex:
        if ex.status_code == 409:
            print("  Role assignment already exists.")
        else:
            raise
