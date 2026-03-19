"""Authentication helpers — credential building, token acquisition, JWT parsing.

Supports three credential modes:

1. ``--managed-identity-client-id`` → ManagedIdentityCredential
2. ``--interactive`` → InteractiveBrowserCredential
3. (default) → DefaultAzureCredential
"""

from __future__ import annotations

import base64
import json
import sys

from azure.identity import (
    DefaultAzureCredential,
    InteractiveBrowserCredential,
    ManagedIdentityCredential,
)

# Scope constants
FABRIC_SCOPE = "https://api.fabric.microsoft.com/.default"
ARM_SCOPE = "https://management.azure.com/.default"


def build_credential(
    tenant: str | None = None,
    client_id: str | None = None,
    managed_identity_client_id: str | None = None,
    interactive: bool = False,
):
    """Return an Azure ``TokenCredential`` based on CLI flags.

    Priority (first match wins):
      1. *managed_identity_client_id* → ``ManagedIdentityCredential``
      2. *interactive* → ``InteractiveBrowserCredential``
      3. (default) → ``DefaultAzureCredential``
    """

    if managed_identity_client_id:
        return ManagedIdentityCredential(client_id=managed_identity_client_id)

    if interactive:
        kwargs: dict = {}
        if tenant:
            kwargs["tenant_id"] = tenant
        if client_id:
            kwargs["client_id"] = client_id
        return InteractiveBrowserCredential(**kwargs)

    # Default: env vars → workload identity → managed identity →
    # Azure CLI → azd → Azure PowerShell → VS Code
    kwargs = {}
    if tenant:
        kwargs["tenant_id"] = tenant
    return DefaultAzureCredential(**kwargs)


def acquire_token(credential, scope: str = FABRIC_SCOPE) -> str:
    """Acquire an access token string for the given scope."""
    token = credential.get_token(scope)
    return token.token


# ---------------------------------------------------------------------------
# JWT claim helpers
# ---------------------------------------------------------------------------

def _b64url_decode(data: str) -> bytes:
    """Decode a base64url-encoded JWT segment."""
    padding = "=" * (-len(data) % 4)
    return base64.urlsafe_b64decode(data + padding)


def _get_token_claims(credential, scope: str) -> dict:
    """Acquire a token and parse the JWT payload claims."""
    token_str = acquire_token(credential, scope)
    parts = token_str.split(".")
    if len(parts) < 2:
        raise ValueError("Invalid JWT format")
    payload = _b64url_decode(parts[1])
    return json.loads(payload)


def get_principal_object_id(credential) -> str:
    """Extract the ``oid`` (object ID) claim from an ARM access token."""
    claims = _get_token_claims(credential, ARM_SCOPE)
    oid = claims.get("oid", "")
    if not oid or not oid.strip():
        raise RuntimeError("JWT token does not contain 'oid' claim")
    return oid.strip()


def get_tenant_id(credential) -> str:
    """Extract the ``tid`` (tenant ID) claim from an ARM access token."""
    claims = _get_token_claims(credential, ARM_SCOPE)
    tid = claims.get("tid", "")
    if not tid or not tid.strip():
        raise RuntimeError("JWT token does not contain 'tid' claim")
    return tid.strip()
