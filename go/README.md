# Cosmos Mirror for GO

A Go CLI tool that automates the creation of [Microsoft Fabric Mirrored Databases](https://learn.microsoft.com/fabric/database/mirrored-database/overview) backed by Azure Cosmos DB. It uses the Fabric REST API to create the mirror artifact and start replication, and optionally configures the Cosmos DB account's networking (RBAC, IP firewall, Network ACL bypass) via the Azure Resource Manager SDK.

## Features

- **Create mirrored databases** — provisions a Fabric Mirrored Database artifact pointing at a Cosmos DB database and starts replication.
- **Auto-discover containers** — optionally mirrors all containers and automatically picks up new ones added later.
- **Selective container mirroring** — mirror only specific containers by name.
- **Workspace folder placement** — place the mirror artifact in a specific Fabric workspace folder.
- **VNet / private-endpoint configuration** (`--configure-vnet`) — automates the full networking setup required for mirroring a Cosmos DB account that has public access disabled:
  - **RBAC** — creates a custom `FabricMirroringRole` (with `readMetadata` and `readAnalytics` data actions) and assigns it plus the built-in Data Contributor role.
  - **IP Firewall** — retrieves Azure service tags via the ARM API, extracts the regional DataFactory IPv4 prefixes and the **global** PowerQueryOnline IPv4 prefixes (PowerQuery does not run in every region, so the full global list is required), and temporarily merges them into the account's IP firewall rules for the duration of mirroring setup.
  - **Network ACL** — enables the `EnableFabricNetworkAclBypass` capability and registers the Fabric workspace resource ID.
  - **Restore** — after mirroring starts, reverts the account's IP rules and public-network-access setting back to their original state.
- **Flexible authentication** — DefaultAzureCredential (default), interactive browser sign-in, or managed identity.

## Prerequisites

1. **Go 1.24+** — [Download](https://go.dev/dl/)
2. **Azure CLI** — [Install](https://learn.microsoft.com/en-us/cli/azure/install-azure-cli). Run `az login` before using the tool (required for DefaultAzureCredential and for `--configure-vnet`).
3. **Fabric workspace role** — the signed-in identity needs **Contributor**, **Member**, or **Admin** role on the target Fabric workspace to create mirrored database items.
4. **A Cosmos DB connection in the Fabric portal** — the Fabric REST API does not currently support creating OAuth2 connections programmatically. Create one manually via **Settings → Manage connections and gateways** in the Fabric portal and pass its display name via `--connection`. Creating a connection requires the user to have a **Fabric (Free or Pro) license** and permission to use cloud connections in the tenant (enabled by default; tenant admins can restrict this under **Tenant settings → Create cloud connections**).
5. **For `--configure-vnet`**: the signed-in identity needs **Contributor** and **User Access Administrator** (or equivalent) on the resource group containing the Cosmos DB account.

## Quick Start

```bash
# Clone and build
cd go/
go mod tidy
go build .
```

### Basic usage (DefaultAzureCredential)

Required parameters only. Uses DefaultAzureCredential. Mirrors all containers in database.

```powershell
go run . `
  --workspace "<My Fabric Workspace>" `
  --connection "<Fabric Connection Name>" `
  --cosmos-endpoint "https://<cosmos-account-name>.documents.azure.com:443/" `
  --database "<MyCosmosDatabase>" `
  --mirror-name "<FabricMirrorArtifactName>"
```

> **Note:** DefaultAzureCredential is used by default — it picks up your
> `az login` / VS Code / environment variable credentials automatically.
> Add `--interactive` to force a browser sign-in instead.

## Examples

### Example 1: Interactive auth — basic usage

Forces a browser sign-in window. Use when DefaultAzureCredential isn't
picking up the right account or you haven't run `az login`.

```powershell
go run . `
  --interactive `
  --workspace "<My Fabric Workspace>" `
  --connection "<Fabric Connection Name>" `
  --cosmos-endpoint "https://<cosmos-account-name>.documents.azure.com:443/" `
  --database "<MyCosmosDatabase>" `
  --mirror-name "<FabricMirrorArtifactName>"
```

### Example 2: With specific containers and folder

Only mirror specific containers. Create Mirror database artifact in specific folder in Workspace (folder must exist)

```powershell
go run . `
  --workspace "<My Fabric Workspace>" `
  --connection "<Fabric Connection Name>" `
  --cosmos-endpoint "https://<cosmos-account-name>.documents.azure.com:443/" `
  --database "<MyCosmosDatabase>" `
  --containers "<MyContainer1>, <MyContainer2>" `
  --mirror-name "<Database Mirror Artifact Name>" `
  --folder "Mirroring"
```

### Example 3: Auto-discover new containers

```powershell
go run . `
  --workspace "<My Fabric Workspace>" `
  --connection "<Fabric Connection Name>" `
  --cosmos-endpoint "https://<cosmos-account-name>.documents.azure.com:443/" `
  --database "<MyCosmosDatabase>" `
  --mirror-name "<Database Mirror Artifact Name>" `
  --auto-discover
```

### Example 4: Specific tenant and app registration

```powershell
go run . `
  --interactive `
  --tenant "YOUR_TENANT_ID" `
  --client "YOUR_CLIENT_ID" `
  --workspace "<My Fabric Workspace>" `
  --connection "<Fabric Connection Name>" `
  --cosmos-endpoint "https://<cosmos-account-name>.documents.azure.com:443/" `
  --database "<MyCosmosDatabase>" `
  --mirror-name "<Database Mirror Artifact Name>"
```

### Example 5: Managed Identity (unattended)

When running on Azure-hosted compute with a user-assigned managed identity,
provide the identity's client ID. No browser prompt is needed.

```powershell
go run . `
  --managed-identity-client-id "YOUR_MANAGED_IDENTITY_CLIENT_ID" `
  --workspace "<My Fabric Workspace>" `
  --connection "<Fabric Connection Name>" `
  --cosmos-endpoint "https://<cosmos-account-name>.documents.azure.com:443/" `
  --database "<MyCosmosDatabase>" `
  --mirror-name "<Database Mirror Artifact Name>"
```

### Example 6: Configure Cosmos Mirroring with VNet or Private Link

Configures Cosmos DB account for Mirroring when using Virtual Networking or
Private Link. Configures RBAC policies on Cosmos account, configures network settings, creates the mirror, starts replication, then restores the original network settings.

Requires `--subscription` and `--resource-group`.

```powershell
go run . `
  --configure-vnet `
  --subscription "<SUBSCRIPTION_ID>" `
  --resource-group "<RESOURCE_GROUP>" `
  --workspace "<My Fabric Workspace>" `
  --connection "<Fabric Connection Name>" `
  --cosmos-endpoint "https://<cosmos-account-name>.documents.azure.com:443/" `
  --database "<MyCosmosDatabase>" `
  --mirror-name "<Database Mirror Artifact Name>"
  --folder "Mirroring" `
  --auto-discover
```

### Example 7: VNet setup — explicit principal ID

```powershell
go run . `
  --configure-vnet `
  --principal-id "YOUR_PRINCIPAL_OBJECT_ID" `
  --subscription "<SUBSCRIPTION_ID>" `
  --resource-group "<RESOURCE_GROUP>" `
  --workspace "<My Fabric Workspace>" `
  --connection "<Fabric Connection Name>" `
  --cosmos-endpoint "https://<cosmos-account-name>.documents.azure.com:443/" `
  --database "<MyCosmosDatabase>" `
  --mirror-name "<Database Mirror Artifact Name>"
  --folder "Mirroring" `
  --auto-discover
```

## Flags Reference

### Required

| Flag | Description |
|------|-------------|
| `--workspace` | Fabric workspace name |
| `--cosmos-endpoint` | Cosmos DB account endpoint |
| `--database` | Cosmos DB database name |
| `--connection` | Display name of a pre-existing Fabric connection |
| `--mirror-name` | Display name for the mirrored database artifact |

### Optional — General

| Flag | Description |
|------|-------------|
| `--tenant` | Entra tenant ID (narrows credential selection) |
| `--client` | App registration client ID (used with `--interactive`) |
| `--interactive` | Force interactive browser sign-in |
| `--managed-identity-client-id` | User-assigned managed identity client ID |
| `--folder` | Workspace folder to place the artifact in |
| `--containers` | Comma-separated list of containers to mirror |
| `--auto-discover` | Auto-mirror new containers (default: false) |

### Optional — VNet Configuration (`--configure-vnet`)

| Flag | Description |
|------|-------------|
| `--configure-vnet` | Enable Cosmos DB networking setup (RBAC, firewall, ACL) |
| `--subscription` | Azure subscription ID (required with `--configure-vnet`) |
| `--resource-group` | Resource group name (required with `--configure-vnet`) |
| `--principal-id` | User/SP object ID for RBAC (auto-detected if omitted) |
| `--skip-rbac` | Skip RBAC role/assignment creation |
| `--skip-firewall` | Skip IP firewall configuration |
| `--skip-network-acl` | Skip Network ACL bypass configuration |
| `--no-restore` | Keep temporary network settings after mirror creation |

## Authentication

The tool supports three authentication modes, selected by flag priority:

| Priority | Flag | Credential |
|----------|------|------------|
| 1 | `--managed-identity-client-id` | `ManagedIdentityCredential` |
| 2 | `--interactive` | `InteractiveBrowserCredential` |
| 3 | *(default — no flag)* | `DefaultAzureCredential` |

**DefaultAzureCredential** tries, in order: environment variables → workload identity → managed identity → Azure CLI → azd → Azure PowerShell → VS Code.

## VNet Configuration Flow

When `--configure-vnet` is specified, the tool performs the following steps
before and after creating the Fabric mirror:

```
Phase 0 ── Capture initial account state (IP rules, public access)
Step  1 ── RBAC: create and assign custom FabricMirroringRole + assign Data Contributor
Step  2 ── IP Firewall: fetch service tags via ARM API, merge Fabric IPs temporarily
Step  3 ── Network ACL: enable capability, set Fabric workspace ID
Step  4 ── Create Fabric Mirror + start mirroring (always runs)
Step  5 ── Restore original IP rules and network access (unless --no-restore)
```

Individual steps can be skipped with `--skip-rbac`, `--skip-firewall`, or `--skip-network-acl`. Use `--no-restore` to keep the temporary network settings in place after mirror creation.

## Project Structure

```
go/
├── main.go            # CLI entry point, flag parsing, orchestration
├── auth.go            # Credential building, token acquisition, JWT parsing
├── helpers.go         # Constants, HTTP helpers, workspace/connection/folder lookup
├── vnet.go            # VNet orchestration, account state capture/restore
├── rbac.go            # Cosmos SQL RBAC role creation and assignment
├── servicetags.go     # Azure service tag download, IP extraction, firewall config
├── network_acl.go     # Network ACL bypass capability and workspace registration
├── go_run.txt         # PowerShell run examples (copy-paste ready)
├── futures/           # Deferred features (connection creation)
│   └── CONNECTION_CREATION.md
└── debugging/         # Ad-hoc diagnostic scripts
```

## Known Limitations

- **OAuth2 connections cannot be created programmatically** — the Fabric REST API does not support the OAuth2 credential type. Connections must be created manually in the Fabric portal before using this utility. See [`futures/CONNECTION_CREATION.md`](futures/CONNECTION_CREATION.md).
- **ARM account updates are slow** — Cosmos DB networking changes (IP firewall, ACL bypass) can take 5–15 minutes each. The tool waits for completion before proceeding.

## License

This project is provided as a sample. See your organization's policies for usage guidelines.
