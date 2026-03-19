"""Mirrored database creation and replication start.

Creates the Fabric Mirrored Database artifact and starts replication with
retry handling.
"""

from __future__ import annotations

import base64
import json
import sys
import time

import requests

from fabric_client import FABRIC_BASE, do_post


def create_mirrored_database(token: str, workspace_id: str, connection_id: str,
                             database: str, display_name: str,
                             folder_id: str | None, auto_discover: bool,
                             container_list: list[str]) -> str:
    """Create a Fabric Mirrored Database item backed by a Cosmos DB connection.

    Args:
        token: Fabric API access token.
        workspace_id: Target Fabric workspace ID.
        connection_id: Fabric connection ID (pre-existing).
        database: Cosmos DB database name.
        display_name: Display name for the mirrored database artifact.
        folder_id: Optional workspace folder ID.
        auto_discover: Whether to auto-mirror new containers.
        container_list: Optional list of specific container names.

    Returns:
        The mirrored database item ID.
    """

    # Build the mirroring.json payload per the documented schema:
    # https://learn.microsoft.com/en-us/fabric/database/mirrored-database/mirrored-database-rest-api
    source_type_props: dict = {
        "connection": connection_id,
        "database": database,
    }

    mirroring_def: dict = {
        "properties": {
            "source": {
                "type": "CosmosDb",
                "typeProperties": source_type_props,
            },
            "target": {
                "type": "MountedRelationalDatabase",
                "typeProperties": {
                    "defaultSchema": "dbo",
                    "format": "Delta",
                },
            },
        },
    }

    # Optionally restrict to specific containers/tables
    if container_list:
        mounted_tables = [
            {
                "source": {
                    "typeProperties": {
                        "schemaName": "dbo",
                        "tableName": name,
                    }
                }
            }
            for name in container_list
        ]
        mirroring_def["properties"]["mountedTables"] = mounted_tables

    def_json = json.dumps(mirroring_def)
    print("mirroring.json payload:")
    print(def_json)
    encoded = base64.b64encode(def_json.encode()).decode()

    body: dict = {
        "displayName": display_name,
        "definition": {
            "parts": [
                {
                    "path": "mirroring.json",
                    "payload": encoded,
                    "payloadType": "InlineBase64",
                }
            ]
        },
    }

    # Place the artifact in a specific folder (optional)
    if folder_id:
        body["folderId"] = folder_id

    url = f"{FABRIC_BASE}/workspaces/{workspace_id}/mirroredDatabases"
    return do_post(token, url, body)


def start_mirroring(token: str, workspace_id: str, mirror_id: str) -> None:
    """Start replication for a mirrored database, retrying if still initializing."""
    url = (
        f"{FABRIC_BASE}/workspaces/{workspace_id}"
        f"/mirroredDatabases/{mirror_id}/startMirroring"
    )

    max_retries = 12
    for attempt in range(1, max_retries + 1):
        resp = requests.post(url, headers={"Authorization": f"Bearer {token}"})

        if resp.status_code in (200, 202):
            return

        # Retry if the mirrored database is still initializing
        if resp.status_code == 400 and "Initializing" in resp.text:
            print(f"Mirrored database still initializing, retrying in 10s "
                  f"(attempt {attempt}/{max_retries})...")
            time.sleep(10)
            continue

        print(f"ERROR: Start mirroring failed ({resp.status_code}): {resp.text}",
              file=sys.stderr)
        sys.exit(1)

    print("ERROR: Start mirroring timed out — database did not finish initializing",
          file=sys.stderr)
    sys.exit(1)
