"""Azure service tag retrieval and IP firewall configuration (Step 2).

Fetches service tags via the ARM Service Tags REST API and extracts the
DataFactory + PowerQueryOnline IPv4 prefixes needed for Fabric mirroring.
"""

from __future__ import annotations

import sys

import requests

from auth import ARM_SCOPE
from fabric_client import normalize_location


# ---------------------------------------------------------------------------
# Fetch service tags via ARM REST API
# ---------------------------------------------------------------------------

def fetch_service_tags(credential, subscription_id: str, location: str) -> dict:
    """Retrieve Azure service tags using the ARM Service Tags API.

    See: https://learn.microsoft.com/en-us/rest/api/virtualnetwork/service-tags/list
    """
    token = credential.get_token(ARM_SCOPE).token

    api_url = (
        f"https://management.azure.com/subscriptions/{subscription_id}"
        f"/providers/Microsoft.Network/locations/{location}"
        f"/serviceTags?api-version=2024-05-01"
    )

    resp = requests.get(api_url, headers={"Authorization": f"Bearer {token}"})
    if resp.status_code != 200:
        print(f"ERROR: Service Tags API returned {resp.status_code}: {resp.text}", file=sys.stderr)
        sys.exit(1)

    data = resp.json()
    print(f"Loaded {len(data.get('values', []))} service-tag entries "
          f"(cloud: {data.get('cloud', '?')}) from ARM API")
    return data


# ---------------------------------------------------------------------------
# IP extraction
# ---------------------------------------------------------------------------

def extract_fabric_ips(tags: dict, location: str) -> list[str]:
    """Return the IPv4 prefixes required for Fabric mirroring.

    Extracts:
      - **DataFactory.<region>** — regional IPs for the Cosmos DB account's
        location.
      - **PowerQueryOnline** — the GLOBAL list of IPv4 IPs. PowerQuery does
        not run in every Azure region, so regional filtering is not possible;
        the full set of PowerQueryOnline addresses must be allowed.

    These IPs are added to the Cosmos DB account's IP firewall temporarily
    during mirroring setup. After the mirror is created and replication has
    started, the original firewall rules are restored (unless ``--no-restore``).
    """
    normalized_loc = normalize_location(location)
    ips: list[str] = []
    found_data_factory = False

    for entry in tags.get("values", []):
        props = entry.get("properties", {})
        system_service = props.get("systemService", "")
        prefixes = props.get("addressPrefixes", [])

        # DataFactory — match the regional tag (IPv4 only)
        if system_service.lower() == "datafactory":
            tag_region = normalize_location(props.get("region", ""))
            if tag_region == normalized_loc:
                ipv4 = [p for p in prefixes if ":" not in p]
                ips.extend(ipv4)
                print(f"Found {entry.get('name')} with {len(ipv4)} IPv4 prefixes "
                      f"(of {len(prefixes)} total)")
                found_data_factory = True

        # PowerQueryOnline — global IPv4 addresses
        if entry.get("name", "").lower() == "powerqueryonline":
            ipv4 = [p for p in prefixes if ":" not in p]
            ips.extend(ipv4)
            print(f"Found PowerQueryOnline with {len(ipv4)} IPv4 prefixes")

    if not found_data_factory:
        print(f"WARNING: No DataFactory service tag found for region '{location}'")
        print("Available DataFactory regions:")
        for entry in tags.get("values", []):
            props = entry.get("properties", {})
            if props.get("systemService", "").lower() == "datafactory" and props.get("region"):
                print(f"  {entry.get('name')} (region: {props['region']})")

    print(f"Total Fabric IP prefixes: {len(ips)}")
    return ips


# ---------------------------------------------------------------------------
# IP firewall configuration (Step 2)
# ---------------------------------------------------------------------------

def configure_ip_firewall(credential, cosmos_client, subscription_id: str,
                          rg: str, account_name: str, location: str,
                          state) -> None:
    """Merge Fabric service-tag IPs into the Cosmos DB account's firewall rules.

    Args:
        credential: Azure TokenCredential.
        cosmos_client: ``CosmosDBManagementClient`` instance.
        subscription_id: Azure subscription ID.
        rg: Resource group name.
        account_name: Cosmos DB account name.
        location: Normalized Azure location.
        state: Captured ``AccountState`` with original IP rules.
    """
    from azure.mgmt.cosmosdb.models import (
        DatabaseAccountUpdateParameters,
        IPAddressOrRange,
    )

    print("\n── Step 2: IP Firewall Configuration ────────────────────────────")

    # Fetch service tags via ARM API and extract IPs
    tags = fetch_service_tags(credential, subscription_id, location)
    fabric_ips = extract_fabric_ips(tags, location)
    if not fabric_ips:
        print("ERROR: No Fabric IP prefixes found — cannot configure firewall", file=sys.stderr)
        sys.exit(1)

    # Merge: existing IPs + Fabric IPs (deduplicated)
    ip_set: set[str] = set()
    for rule in state.ip_rules:
        if rule.ip_address_or_range:
            ip_set.add(rule.ip_address_or_range)
    for ip in fabric_ips:
        ip_set.add(ip)

    merged_rules = [IPAddressOrRange(ip_address_or_range=ip) for ip in ip_set]

    print(f"Existing IP rules: {len(state.ip_rules)}, "
          f"Fabric IPs: {len(fabric_ips)}, Merged total: {len(merged_rules)}")

    # Update account — enable public network access + merged IP rules
    params = DatabaseAccountUpdateParameters(
        public_network_access="Enabled",
        ip_rules=merged_rules,
    )

    print("Updating Cosmos DB IP firewall (this may take several minutes)...")
    poller = cosmos_client.database_accounts.begin_update(rg, account_name, params)
    poller.result()
    print("IP firewall configured successfully.")
