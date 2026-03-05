# Fabric Cosmos DB Mirroring Samples

CLI tools that automate the creation of [Microsoft Fabric Mirrored Databases](https://learn.microsoft.com/fabric/database/mirrored-database/overview) backed by Azure Cosmos DB. Each sample uses the Fabric REST API to create the mirror artifact and start replication, and optionally configures the Cosmos DB account's networking (RBAC, IP firewall, Network ACL bypass) via the Azure Resource Manager SDK.

## Languages available

The samples are available in the following languages:

- [Go](go/README.md)
- [Python](python/README.md) *(coming soon)*
- [C#](csharp/README.md) *(coming soon)*

## VS Code debugging (recommended)

This repo contains samples in multiple languages/frameworks. Use the `*.code-workspace` files at the repo root to open and debug a single language sample at a time.

Each workspace file opens only that language's folder (for example, `Go.code-workspace` opens `go/`). This keeps debug configuration, settings, and environment-file handling isolated per sample.

To avoid VS Code debug adapter errors when you don't have every language extension installed, open the workspace file for the language you're working on:

- [Go.code-workspace](Go.code-workspace)
- [Python.code-workspace](Python.code-workspace)
- [Csharp.code-workspace](Csharp.code-workspace)

### How to open a workspace file

In VS Code:

1. Use **File → Open Workspace from File…**
2. Pick the `*.code-workspace` file you want (for example, `Go.code-workspace`).

Then use **Run and Debug** (or press **F5**) to start that sample.

**Notes:**

- Opening the repo as a normal folder is fine for browsing, but the run/debug configurations are intentionally isolated per language workspace file.
- If you switch between languages, just open the other workspace file.

## Features

All language implementations share the same core capabilities:

- **Create mirrored databases** — provisions a Fabric Mirrored Database artifact pointing at a Cosmos DB database and starts replication.
- **Auto-discover containers** — optionally mirrors all containers and automatically picks up new ones added later.
- **Selective container mirroring** — mirror only specific containers by name.
- **Workspace folder placement** — place the mirror artifact in a specific Fabric workspace folder.
- **VNet / private-endpoint configuration** (`--configure-vnet`) — automates the full networking setup required for mirroring a Cosmos DB account that has public access disabled.
- **Flexible authentication** — DefaultAzureCredential (default), interactive browser sign-in, or managed identity.

See each language's README for full details, flags reference, and examples.

## License

This project is provided as a sample. See your organization's policies for usage guidelines.
