package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

func main() {
	// ── Authentication flags ──────────────────────────────────────────────
	tenant := flag.String("tenant", "", "Entra tenant ID (optional; narrows credential selection)")
	clientID := flag.String("client", "", "App registration client ID (optional; used with --interactive)")
	managedIdentityClientID := flag.String("managed-identity-client-id", "",
		"User-assigned managed identity client ID (use on Azure-hosted compute)")
	useInteractive := flag.Bool("interactive", false,
		"Use interactive browser sign-in instead of DefaultAzureCredential")

	// ── Fabric workspace ──────────────────────────────────────────────────
	workspaceName := flag.String("workspace", "", "Fabric workspace name (required)")

	// ── Cosmos DB source ──────────────────────────────────────────────────
	cosmosEndpoint := flag.String("cosmos-endpoint", "", "Cosmos DB account endpoint (required)")
	database := flag.String("database", "", "Cosmos DB database name (required)")

	// ── Connection ────────────────────────────────────────────────────────
	//
	// A Cosmos DB connection with OAuth2 credentials must be created in the
	// Fabric portal before running this script. Provide the connection's
	// display name here.
	//
	// NOTE: The Fabric REST API does not currently support creating OAuth2
	// connections programmatically. This is expected to be supported via
	// Workspace Identity in the future. See CONNECTION_CREATION.md for
	// details and context.
	connectionName := flag.String("connection", "",
		"Name of a pre-existing Fabric connection (required — create via Fabric portal)")

	// ── Mirrored database ─────────────────────────────────────────────────
	folderName := flag.String("folder", "",
		"Fabric workspace folder name to place the mirrored database in (optional)")
	mirrorName := flag.String("mirror-name", "",
		"Display name for the mirrored database artifact (required)")
	containers := flag.String("containers", "",
		"Comma-separated list of container names to mirror (optional; when omitted, all containers are mirrored)")
	autoDiscover := flag.Bool("auto-discover", false,
		"Automatically mirror new containers added to the Cosmos DB database (default: false)")

	// ── VNet configuration (Steps 1-3, 5 from PS script) ─────────────────
	configureVNet := flag.Bool("configure-vnet", false,
		"Configure Cosmos DB networking for Fabric mirroring (RBAC, IP firewall, ACL bypass)")
	subscriptionID := flag.String("subscription", "",
		"Azure subscription ID (required with --configure-vnet)")
	resourceGroup := flag.String("resource-group", "",
		"Resource group containing the Cosmos DB account (required with --configure-vnet)")
	principalIDFlag := flag.String("principal-id", "",
		"Service principal/user object ID for RBAC (auto-detected if omitted)")
	skipRBAC := flag.Bool("skip-rbac", false,
		"Skip RBAC setup when using --configure-vnet")
	skipFirewall := flag.Bool("skip-firewall", false,
		"Skip IP firewall configuration when using --configure-vnet")
	skipNetworkACL := flag.Bool("skip-network-acl", false,
		"Skip Network ACL bypass when using --configure-vnet")
	noRestore := flag.Bool("no-restore", false,
		"Don't restore original network settings after mirror creation")
	flag.Parse()

	// ── Validate required flags ───────────────────────────────────────────
	if *workspaceName == "" || *cosmosEndpoint == "" || *database == "" || *mirrorName == "" || *connectionName == "" {
		flag.Usage()
		log.Fatal("--workspace, --cosmos-endpoint, --database, --mirror-name, and --connection are required")
	}

	// Validate VNet flags
	var resolvedAccountName string
	if *configureVNet {
		if *subscriptionID == "" || *resourceGroup == "" {
			flag.Usage()
			log.Fatal("--subscription and --resource-group are required with --configure-vnet")
		}
		resolvedAccountName = deriveAccountName(*cosmosEndpoint)
		if resolvedAccountName == "" {
			log.Fatal("Could not derive account name from --cosmos-endpoint")
		}
		fmt.Println("Derived account name:", resolvedAccountName)
	}

	// Parse optional container list
	var containerList []string
	if *containers != "" {
		for _, c := range strings.Split(*containers, ",") {
			c = strings.TrimSpace(c)
			if c != "" {
				containerList = append(containerList, c)
			}
		}
	}

	ctx := context.Background()

	// Authenticate – DefaultAzureCredential (default), interactive browser, or managed identity
	cred := buildCredential(*tenant, *clientID, *managedIdentityClientID, *useInteractive)
	token := acquireToken(ctx, cred)

	client := &http.Client{}

	// 1. Resolve workspace by name
	workspaceID := lookupWorkspace(ctx, client, token, *workspaceName)
	fmt.Println("Workspace ID:", workspaceID)

	// 2. Configure VNet networking (Steps 1-3) — optional
	var initialState *AccountState
	if *configureVNet {
		initialState = runVNetSetup(ctx, cred, *subscriptionID, *resourceGroup,
			resolvedAccountName, *principalIDFlag, workspaceID, *tenant,
			*skipRBAC, *skipFirewall, *skipNetworkACL)
		fmt.Println("\n── Step 4: Create Fabric Mirror ──────────────────────────────────")
	}

	// 3. Look up the pre-existing connection by display name
	connectionID := lookupConnection(ctx, client, token, *connectionName)
	fmt.Println("Using connection:", connectionID)

	// 4. Optionally resolve folder by name
	var folderID string
	if *folderName != "" {
		folderID = lookupFolder(ctx, client, token, workspaceID, *folderName)
		fmt.Println("Folder ID:", folderID)
	}

	// 5. Create the mirrored database item
	mirrorID := createMirroredDatabase(ctx, client, token, workspaceID, connectionID, *database, *mirrorName, folderID, *autoDiscover, containerList)
	fmt.Println("Mirrored Database ID:", mirrorID)

	// 6. Start mirroring
	startMirroring(ctx, client, token, workspaceID, mirrorID)
	fmt.Println("Mirroring started successfully")

	// 7. Restore network settings (Step 5) — optional
	if *configureVNet && !*noRestore && initialState != nil {
		accountClient := newAccountsClient(*subscriptionID, cred)
		restoreNetworkSettings(ctx, accountClient, *resourceGroup, resolvedAccountName, initialState)
	}
}

// ---------------------------------------------------------------------------
// Mirrored database creation
// ---------------------------------------------------------------------------

// createMirroredDatabase creates a Fabric Mirrored Database item backed by
// the given Cosmos DB connection.
//
// autoDiscover controls the "autoDiscoverNewCollections" property in the
// mirroring definition. When true, Fabric will automatically start mirroring
// any new containers added to the Cosmos DB database after initial setup.
//
// containerList optionally restricts mirroring to specific containers. When
// empty, all existing containers are mirrored. You can combine a non-empty
// list with autoDiscover=true to mirror specific containers now AND
// automatically pick up new ones later.
func createMirroredDatabase(ctx context.Context, client *http.Client, token, workspaceID, connectionID, db, displayName, folderID string, autoDiscover bool, containerList []string) string {
	// Build the mirroring.json payload per the documented schema:
	// https://learn.microsoft.com/en-us/fabric/database/mirrored-database/mirrored-database-rest-api
	sourceTypeProps := map[string]interface{}{
		"connection": connectionID,
		"database":   db,
	}

	mirroringDef := map[string]interface{}{
		"properties": map[string]interface{}{
			"source": map[string]interface{}{
				"type":           "CosmosDb",
				"typeProperties": sourceTypeProps,
			},
			"target": map[string]interface{}{
				"type": "MountedRelationalDatabase",
				"typeProperties": map[string]interface{}{
					"defaultSchema": "dbo",
					"format":        "Delta",
				},
			},
		},
	}

	// Optionally restrict to specific containers/tables
	if len(containerList) > 0 {
		var mountedTables []map[string]interface{}
		for _, name := range containerList {
			mountedTables = append(mountedTables, map[string]interface{}{
				"source": map[string]interface{}{
					"typeProperties": map[string]interface{}{
						"schemaName": "dbo",
						"tableName":  name,
					},
				},
			})
		}
		mirroringDef["properties"].(map[string]interface{})["mountedTables"] = mountedTables
	}

	defJSON, _ := json.Marshal(mirroringDef)
	fmt.Println("mirroring.json payload:")
	fmt.Println(string(defJSON))
	encoded := base64.StdEncoding.EncodeToString(defJSON)

	body := map[string]interface{}{
		"displayName": displayName,
		"definition": map[string]interface{}{
			"parts": []map[string]string{
				{
					"path":        "mirroring.json",
					"payload":     encoded,
					"payloadType": "InlineBase64",
				},
			},
		},
	}

	// Place the artifact in a specific folder (optional)
	if folderID != "" {
		body["folderId"] = folderID
	}

	url := fmt.Sprintf("%s/workspaces/%s/mirroredDatabases", fabricBase, workspaceID)
	return doPost(ctx, client, token, url, body)
}

// startMirroring initiates the replication process for a mirrored database.
// It retries if the database is still initializing.
func startMirroring(ctx context.Context, client *http.Client, token, workspaceID, mirrorID string) {
	url := fmt.Sprintf("%s/workspaces/%s/mirroredDatabases/%s/startMirroring",
		fabricBase, workspaceID, mirrorID)

	const maxRetries = 12
	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, "POST", url, nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		if err != nil {
			log.Fatalf("Failed to start mirroring: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
			return
		}

		// Retry if the mirrored database is still initializing
		if resp.StatusCode == http.StatusBadRequest && strings.Contains(string(body), "Initializing") {
			fmt.Printf("Mirrored database still initializing, retrying in 10s (attempt %d/%d)...\n", attempt, maxRetries)
			time.Sleep(10 * time.Second)
			continue
		}

		log.Fatalf("Start mirroring failed (%d): %s", resp.StatusCode, body)
	}
	log.Fatal("Start mirroring timed out — database did not finish initializing")
}
