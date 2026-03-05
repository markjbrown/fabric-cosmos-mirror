# Connection Creation — Status & Roadmap

## Current Requirement

Before running `cosmos-mirror`, you must create a **Cosmos DB connection with
OAuth2 credentials** in the **Fabric portal** manually. The connection's
display name is then passed to the script via `--connection`.

This is a temporary limitation — the Fabric REST API does not yet support
creating OAuth2 connections programmatically.

---

## What Was Tried

We systematically explored every available API surface to automate OAuth2
connection creation. Here is a summary of the approaches attempted and their
outcomes.

### 1. Fabric REST API — `POST /v1/connections`

The documented Fabric API for creating connections accepts the following
credential types in its union schema:

- Anonymous
- Basic
- Key
- ServicePrincipal
- SharedAccessSignature
- WindowsWithoutImpersonation
- WorkspaceIdentity (preview / not universally available)

**OAuth2 is NOT listed** in the credential type union. Attempting to pass
`"credentialType": "OAuth2"` returns:

```
CredentialType input is not supported for this API
```

This was confirmed by querying `GET /v1/connections/supportedConnectionTypes`
and inspecting the credential details for the `CosmosDb` connector.

### 2. Power BI Gateway API — `POST /v2.0/myorg/me/gatewayClusterCloudDatasource`

This is the **undocumented** endpoint that the Fabric portal calls internally
when you create a connection through the UI. We captured the portal's payload
and replicated it in Go.

| Attempt | Approach | Result |
|---------|----------|--------|
| A | `useCallerAADIdentity: true` + PBI bearer token | 400 Bad Request |
| B | `useCallerAADIdentity: true` + Fabric bearer token | 400 Bad Request |
| C | Cosmos access token as `accessToken` credential | "missing redirectEndpoint" |
| D | Full OAuth2 auth code flow with localhost redirect | AADSTS50011 — redirect URI mismatch on the PBI first-party app |
| E | Access token + `skipGetOAuthToken: true` + `skipTestConnection: true` | "InvalidDatasourceTypeForSkipTestConnection" |
| F | Access token + `skipGetOAuthToken: true` + `skipTestConnection: false` + Fabric bearer | Gateway test-connection hit Cosmos firewall (401 from IP 52.150.139.96) |
| G | Same as F but with Cosmos-scoped bearer | 403 — wrong audience for api.powerbi.com |
| H | Same as F but with Power BI-scoped bearer | 403 — empty response body |

**Conclusion:** The Power BI gateway endpoint requires an internal Microsoft
first-party OAuth2 flow (redirect through `login.microsoftonline.com` with a
first-party client ID and its pre-registered redirect URIs). This flow is not
reproducible from external tooling.

### 3. Device Code Flow

Not available — device code authentication is not permitted in this
environment.

---

## Experimental Code

The attempted connection creation code is preserved in
[`connection_create_future.go`](connection_create_future.go) with a
`//go:build future` build constraint. It is **excluded from normal builds**.

To review or test it:

```bash
go build -tags future .
```

---

## Future: Workspace Identity

The Fabric team has indicated that **Workspace Identity** will eventually
enable automated OAuth2 connection creation. When available:

1. The `POST /v1/connections` API will accept `"credentialType": "WorkspaceIdentity"`.
2. The connection will authenticate to Cosmos DB using the workspace's
   managed identity instead of a user's OAuth2 delegation.
3. No interactive browser prompt will be needed.

### What This Means for This Script

When Workspace Identity becomes GA:

- Add a `--workspace-identity` flag to `cosmos-mirror`.
- Call `POST /v1/connections` with credential type `WorkspaceIdentity`.
- The `--connection` flag becomes optional (either use an existing one or
  create a new one via the API).

### Tracking

- [Fabric Workspace Identity documentation](https://learn.microsoft.com/en-us/fabric/security/workspace-identity)
- [Fabric REST API — Connections](https://learn.microsoft.com/en-us/rest/api/fabric/core/connections)

---

## Private Link / VNet Configuration

For Cosmos DB accounts configured with Private Link or Virtual Network
restrictions, additional network configuration is required before Fabric
can reach the account. A companion PowerShell script will handle:

- Enabling managed private endpoints in the Fabric workspace
- Approving the private endpoint connection on the Cosmos DB account
- Verifying connectivity

This is a separate concern from connection creation and will be documented
alongside the PowerShell script.
