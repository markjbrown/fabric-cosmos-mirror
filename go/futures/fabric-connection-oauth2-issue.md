# Fabric REST API – Cannot Create Cosmos DB Connection with OAuth2

## Summary

We are trying to programmatically create a **Cosmos DB connection** in Microsoft Fabric using the
[Create Connection REST API](https://learn.microsoft.com/en-us/rest/api/fabric/core/connections/create-connection).
The connection works when created through the **Fabric UI** (Manage Connections and Gateways), but
the REST API rejects every OAuth2 payload we have tried.

## What works (Fabric UI)

When creating a Cosmos DB mirroring connection through the Fabric portal, you can choose
**"Organizational account"** (Entra ID / OAuth2) authentication. The resulting connection object
returned by `GET /v1/connections` looks like this:

```json
{
  "id": "f6e3cd91-d4a4-451a-9265-be6611902ef8",
  "displayName": "cosmos-mirror-test mjbrown",
  "connectivityType": "ShareableCloud",
  "connectionDetails": {
    "type": "CosmosDB",
    "path": "{\"host\":\"https://cosmos-mirror-test.documents.azure.com:443/\"}"
  },
  "credentialDetails": {
    "credentialType": "OAuth2",
    "singleSignOnType": "None",
    "connectionEncryption": "NotEncrypted"
  }
}
```

All 8 of my existing Cosmos DB connections were created this way — all use `CosmosDB` type,
`OAuth2` credential, `ShareableCloud` connectivity, and `NotEncrypted` encryption.

## What the discovery API tells us

`GET /v1/connections/supportedConnectionTypes` reports that the `CosmosDB` type supports:

- **Credential types:** `Key`, `OAuth2`, `WorkspaceIdentity`
- **Creation method:** `CosmosDB.Contents`
- **Required parameter:** `host`

So OAuth2 *should* be a valid credential type.

## The request we are making

```
POST https://api.fabric.microsoft.com/v1/connections
Authorization: Bearer <fabric-api-token>
Content-Type: application/json
```

### Attempt 1 — OAuth2, ShareableCloud (no access token)

```json
{
  "connectivityType": "ShareableCloud",
  "displayName": "cosmos-CosmosMirrorPublicDB-connection",
  "connectionDetails": {
    "type": "CosmosDB",
    "creationMethod": "CosmosDB.Contents",
    "parameters": [
      {
        "dataType": "Text",
        "name": "host",
        "value": "https://mjb-cosmos-mirror-wsus.documents.azure.com:443/"
      }
    ]
  },
  "credentialDetails": {
    "connectionEncryption": "NotEncrypted",
    "singleSignOnType": "None",
    "skipTestConnection": false,
    "credentials": {
      "credentialType": "OAuth2"
    }
  },
  "privacyLevel": "Organizational"
}
```

**Error response (400):**
```json
{
  "errorCode": "InvalidParameter",
  "message": "CredentialType input is not supported for this API."
}
```

### Attempt 2 — OAuth2, ShareableCloud (with Cosmos DB access token)

We acquired a Cosmos DB-scoped Entra ID token (`https://cosmos.azure.com/.default`) and included
it in the credentials:

```json
{
  "connectivityType": "ShareableCloud",
  "displayName": "cosmos-CosmosMirrorPublicDB-connection",
  "connectionDetails": {
    "type": "CosmosDB",
    "creationMethod": "CosmosDB.Contents",
    "parameters": [
      {
        "dataType": "Text",
        "name": "host",
        "value": "https://mjb-cosmos-mirror-wsus.documents.azure.com:443/"
      }
    ]
  },
  "credentialDetails": {
    "connectionEncryption": "NotEncrypted",
    "singleSignOnType": "None",
    "skipTestConnection": false,
    "credentials": {
      "credentialType": "OAuth2",
      "accessToken": "<fabric-scoped-entra-id-token>"
    }
  },
  "privacyLevel": "Organizational"
}
```

**Error response (400):**
```json
{
  "errorCode": "InvalidParameter",
  "message": "CredentialType input is not supported for this API."
}
```

### Attempt 3 — OAuth2, PersonalCloud (with access token)

Since UI-created connections appear as `ShareableCloud`, we also tried `PersonalCloud`:

```json
{
  "connectivityType": "PersonalCloud",
  "displayName": "cosmos-CosmosMirrorPublicDB-connection",
  "connectionDetails": {
    "type": "CosmosDB",
    "creationMethod": "CosmosDB.Contents",
    "parameters": [
      {
        "dataType": "Text",
        "name": "host",
        "value": "https://mjb-cosmos-mirror-wsus.documents.azure.com:443/"
      }
    ]
  },
  "credentialDetails": {
    "connectionEncryption": "NotEncrypted",
    "singleSignOnType": "None",
    "skipTestConnection": false,
    "credentials": {
      "credentialType": "OAuth2",
      "accessToken": "<cosmos-db-scoped-entra-id-token>"
    }
  },
  "privacyLevel": "Organizational"
}
```

**Error response (400):**
```json
{
  "errorCode": "InvalidParameter",
  "message": "ConnectivityType input is not supported for this API."
}
```

### Other variations tried (all failed)

| Variation | Error |
|---|---|
| `credentialType` at top level of `credentialDetails` instead of nested in `credentials` | "The Credentials field is required" |
| `singleSignOnType: "MicrosoftEntraID"` | Same "CredentialType input is not supported" |
| `skipTestConnection: true` | Same error |
| Empty `credentials: {}` | 3× generic "InvalidParameter" errors |
| Omit `credentials` entirely (only `connectionEncryption`, `singleSignOnType`, `skipTestConnection`) | "The Credentials field is required" |
| `accessToken` set to Fabric-audience token (`https://api.fabric.microsoft.com/.default`) instead of Cosmos-scoped | Same "CredentialType input is not supported" |
| `accessToken` set to Cosmos-audience token (`https://cosmos.azure.com/.default`) | Same "CredentialType input is not supported" |
| OAuth2 with `skipTestConnection: true` and no `accessToken` | Same "CredentialType input is not supported" |

## Questions

1. **Is `OAuth2` supported as a credential type for `POST /v1/connections`?**
   The `GET /v1/connections/supportedConnectionTypes` endpoint lists `OAuth2` as supported for
   `CosmosDB`, but every attempt to use it returns `"CredentialType input is not supported for
   this API."` Does the Create Connection API require an interactive browser redirect flow for
   OAuth2 that can't be done via REST?

2. **What is the correct payload format for creating an OAuth2 connection to Cosmos DB via REST?**
   The existing connections (created via UI) all show `credentialType: "OAuth2"` when read back
   via `GET /v1/connections`, so the connection object exists — we just can't create one via the
   API.

3. **Are `Key` or `WorkspaceIdentity` the only credential types that work with `POST /v1/connections` for cloud data sources?**
   If OAuth2 requires a UI flow, what are the recommended alternatives for programmatic connection
   creation?

## Environment

- **Fabric API:** `https://api.fabric.microsoft.com/v1`
- **Auth scope:** `https://api.fabric.microsoft.com/.default`
- **Connection type:** `CosmosDB` (NoSQL — not MongoDB)
- **Cosmos DB endpoint:** `https://mjb-cosmos-mirror-wsus.documents.azure.com:443/`
- **Caller identity:** Interactive Entra ID user token (same user who created the 8 working
  connections via UI)
