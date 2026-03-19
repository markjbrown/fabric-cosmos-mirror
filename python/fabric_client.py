"""Fabric REST API helpers — constants, HTTP utilities, workspace / connection / folder lookup."""

from __future__ import annotations

import json
import time
import sys
from typing import Any

import requests

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

FABRIC_BASE = "https://api.fabric.microsoft.com/v1"


# ---------------------------------------------------------------------------
# Utility helpers
# ---------------------------------------------------------------------------

def derive_account_name(endpoint: str) -> str:
    """Extract the Cosmos DB account name from an endpoint URL.

    Example::

        "https://myaccount.documents.azure.com:443/" → "myaccount"
    """
    host = endpoint
    for prefix in ("https://", "http://"):
        if host.startswith(prefix):
            host = host[len(prefix):]
            break
    # Strip port / path
    for sep in (":", "/"):
        idx = host.find(sep)
        if idx != -1:
            host = host[:idx]
    # Strip domain suffix
    idx = host.find(".")
    if idx != -1:
        return host[:idx]
    return host


def normalize_location(location: str) -> str:
    """Normalize an Azure location string: lowercase, no spaces.

    Example::

        "West Central US" → "westcentralus"
    """
    return location.lower().replace(" ", "")


# ---------------------------------------------------------------------------
# Workspace lookup
# ---------------------------------------------------------------------------

def lookup_workspace(token: str, name: str) -> str:
    """Resolve a Fabric workspace display name to its ID."""
    url = f"{FABRIC_BASE}/workspaces"
    resp = requests.get(url, headers=_auth_header(token))
    _check(resp, "List workspaces")

    for ws in resp.json().get("value", []):
        if ws.get("displayName", "").lower() == name.lower():
            return ws["id"]

    print(f"ERROR: Workspace '{name}' not found", file=sys.stderr)
    sys.exit(1)


# ---------------------------------------------------------------------------
# Connection lookup
# ---------------------------------------------------------------------------

def lookup_connection(token: str, name: str) -> str:
    """Resolve a Fabric connection display name to its ID."""
    url = f"{FABRIC_BASE}/connections"
    resp = requests.get(url, headers=_auth_header(token))
    _check(resp, "List connections")

    for c in resp.json().get("value", []):
        if c.get("displayName", "").lower() == name.lower():
            return c["id"]

    print(f"ERROR: Connection '{name}' not found", file=sys.stderr)
    sys.exit(1)


# ---------------------------------------------------------------------------
# Folder lookup
# ---------------------------------------------------------------------------

def lookup_folder(token: str, workspace_id: str, name: str) -> str:
    """Resolve a folder display name to its ID within a workspace."""
    page_url: str | None = f"{FABRIC_BASE}/workspaces/{workspace_id}/folders"

    while page_url:
        resp = requests.get(page_url, headers=_auth_header(token))
        _check(resp, "List folders")
        data = resp.json()

        for f in data.get("value", []):
            if f.get("displayName", "").lower() == name.lower():
                return f["id"]

        page_url = data.get("continuationUri")

    print(f"ERROR: Folder '{name}' not found in workspace {workspace_id}", file=sys.stderr)
    sys.exit(1)


# ---------------------------------------------------------------------------
# HTTP helpers
# ---------------------------------------------------------------------------

def do_post(token: str, url: str, payload: dict) -> str:
    """Send a JSON POST with Authorization and return the ``id`` from the response.

    Handles 200/201 (immediate) and 202 (long-running operation polling).
    """
    resp = requests.post(
        url,
        headers={**_auth_header(token), "Content-Type": "application/json"},
        json=payload,
    )

    if resp.status_code in (200, 201):
        data = resp.json()
        item_id = data.get("id")
        if item_id:
            return item_id
        print(f"ERROR: No id in response: {resp.text}", file=sys.stderr)
        sys.exit(1)

    if resp.status_code == 202:
        location = resp.headers.get("Location")
        if not location:
            print("ERROR: 202 Accepted but no Location header", file=sys.stderr)
            sys.exit(1)
        return _wait_for_operation(token, location)

    print(f"ERROR: POST {url} failed ({resp.status_code}): {resp.text}", file=sys.stderr)
    sys.exit(1)


def _wait_for_operation(token: str, location: str) -> str:
    """Poll a Fabric long-running operation URL until it succeeds."""
    while True:
        time.sleep(5)
        resp = requests.get(location, headers=_auth_header(token))
        _check(resp, f"Poll operation {location}")

        data = resp.json()
        status = data.get("status", "")

        if status == "Succeeded":
            resource_id = data.get("resourceId", "")
            if resource_id:
                return resource_id
            resource_location = data.get("resourceLocation", "")
            if resource_location:
                return resource_location.rstrip("/").rsplit("/", 1)[-1]
            print(f"ERROR: Operation succeeded but no resource ID found: {resp.text}", file=sys.stderr)
            sys.exit(1)

        if status == "Failed":
            print(f"ERROR: Operation failed: {resp.text}", file=sys.stderr)
            sys.exit(1)

        print(f"Operation status: {status}, waiting...")


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------

def _auth_header(token: str) -> dict[str, str]:
    return {"Authorization": f"Bearer {token}"}


def _check(resp: requests.Response, context: str) -> None:
    """Check HTTP response status and exit on failure."""
    if resp.status_code != 200:
        print(f"ERROR: {context} failed ({resp.status_code}): {resp.text}", file=sys.stderr)
        sys.exit(1)
