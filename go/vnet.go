package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos"
)

// ---------------------------------------------------------------------------
// Account state capture / restore
// ---------------------------------------------------------------------------

// AccountState captures the Cosmos DB account's networking configuration
// before any changes so it can be restored after mirror creation (Step 5).
type AccountState struct {
	PublicNetworkAccess         *armcosmos.PublicNetworkAccess
	IPRules                     []*armcosmos.IPAddressOrRange
	NetworkACLBypass            *armcosmos.NetworkACLBypass
	NetworkACLBypassResourceIDs []*string
	Location                    string
	Capabilities                []*armcosmos.Capability
}

// newAccountsClient creates an ARM Cosmos DB database-accounts client.
func newAccountsClient(subscriptionID string, cred azcore.TokenCredential) *armcosmos.DatabaseAccountsClient {
	client, err := armcosmos.NewDatabaseAccountsClient(subscriptionID, cred, nil)
	if err != nil {
		log.Fatalf("Failed to create DatabaseAccountsClient: %v", err)
	}
	return client
}

// newSQLResourcesClient creates an ARM Cosmos DB SQL-resources client.
func newSQLResourcesClient(subscriptionID string, cred azcore.TokenCredential) *armcosmos.SQLResourcesClient {
	client, err := armcosmos.NewSQLResourcesClient(subscriptionID, cred, nil)
	if err != nil {
		log.Fatalf("Failed to create SQLResourcesClient: %v", err)
	}
	return client
}

// captureAccountState reads the current Cosmos DB account networking config.
func captureAccountState(ctx context.Context, client *armcosmos.DatabaseAccountsClient, rg, account string) *AccountState {
	resp, err := client.Get(ctx, rg, account, nil)
	if err != nil {
		log.Fatalf("Failed to get Cosmos DB account %q: %v", account, err)
	}

	state := &AccountState{}
	if resp.Location != nil {
		state.Location = *resp.Location
	}
	if resp.Properties != nil {
		state.PublicNetworkAccess = resp.Properties.PublicNetworkAccess
		state.IPRules = resp.Properties.IPRules
		state.NetworkACLBypass = resp.Properties.NetworkACLBypass
		state.NetworkACLBypassResourceIDs = resp.Properties.NetworkACLBypassResourceIDs
		state.Capabilities = resp.Properties.Capabilities
	}
	return state
}

// restoreNetworkSettings restores the Cosmos DB account's public-network
// access and IP rules to their original state (Step 5).
//
// Network ACL bypass is intentionally NOT restored; Fabric needs it for
// ongoing access through private endpoints.
func restoreNetworkSettings(ctx context.Context, client *armcosmos.DatabaseAccountsClient, rg, account string, state *AccountState) {
	fmt.Println("\n── Step 5: Restoring network settings ───────────────────────────")

	params := armcosmos.DatabaseAccountUpdateParameters{
		Properties: &armcosmos.DatabaseAccountUpdateProperties{
			PublicNetworkAccess: state.PublicNetworkAccess,
			IPRules:             state.IPRules,
		},
	}

	fmt.Println("Restoring PublicNetworkAccess and IP rules (this may take several minutes)...")
	poller, err := client.BeginUpdate(ctx, rg, account, params, nil)
	if err != nil {
		log.Fatalf("Failed to begin restoring network settings: %v", err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		log.Fatalf("Failed to restore network settings: %v", err)
	}
	fmt.Println("Network settings restored to initial state.")
}

// ---------------------------------------------------------------------------
// VNet setup orchestration
// ---------------------------------------------------------------------------

// runVNetSetup orchestrates the networking setup steps from the PS script:
//   - Step 1 (RBAC)
//   - Step 2 (IP Firewall)
//   - Step 3 (Network ACL Bypass)
//
// Returns the captured initial state for use in Step 5 (restore).
func runVNetSetup(ctx context.Context, cred azcore.TokenCredential,
	subscriptionID, rg, accountName, principalIDOverride, workspaceID, tenantFlag string,
	skipRBAC, skipFirewall, skipNetworkACL bool) *AccountState {

	accountClient := newAccountsClient(subscriptionID, cred)

	// ── Phase 0: Capture initial state ────────────────────────────────────
	fmt.Println("\n── Phase 0: Capturing initial account state ─────────────────────")
	state := captureAccountState(ctx, accountClient, rg, accountName)

	location := normalizeLocation(state.Location)
	fmt.Printf("Account: %s\n", accountName)
	fmt.Printf("Location: %s\n", location)
	if state.PublicNetworkAccess != nil {
		fmt.Printf("PublicNetworkAccess: %s\n", *state.PublicNetworkAccess)
	}
	fmt.Printf("IP rules: %d\n", len(state.IPRules))
	fmt.Printf("Capabilities: %d\n", len(state.Capabilities))

	// ── Determine principal ID ────────────────────────────────────────────
	principalID := principalIDOverride
	if principalID == "" {
		var err error
		principalID, err = getPrincipalObjectID(ctx, cred)
		if err != nil {
			log.Fatalf("Failed to determine principal ID: %v\nProvide --principal-id explicitly.", err)
		}
	}
	fmt.Printf("Principal ID: %s\n", principalID)

	// ── Determine tenant ID (needed for Fabric resource ID in Step 3) ────
	tenantID := tenantFlag
	if tenantID == "" || !isGUID(tenantID) {
		tid, err := getTenantID(ctx, cred)
		if err != nil {
			log.Fatalf("Failed to determine tenant ID: %v\nProvide --tenant with a GUID.", err)
		}
		tenantID = tid
	}
	fmt.Printf("Tenant ID: %s\n", tenantID)

	// ── Step 1: RBAC ─────────────────────────────────────────────────────
	if !skipRBAC {
		sqlClient := newSQLResourcesClient(subscriptionID, cred)
		accountScope := fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DocumentDB/databaseAccounts/%s",
			subscriptionID, rg, accountName)
		setupRBAC(ctx, sqlClient, rg, accountName, accountScope, principalID)
	} else {
		fmt.Println("\n── Step 1: RBAC — skipped (--skip-rbac) ─────────────────────────")
	}

	// ── Step 2: IP Firewall ──────────────────────────────────────────────
	if !skipFirewall {
		configureIPFirewall(ctx, cred, accountClient, subscriptionID, rg, accountName, location, state)
	} else {
		fmt.Println("\n── Step 2: IP Firewall — skipped (--skip-firewall) ──────────────")
	}

	// ── Step 3: Network ACL Bypass ───────────────────────────────────────
	if !skipNetworkACL {
		configureNetworkACLBypass(ctx, accountClient, rg, accountName, workspaceID, tenantID, state)
	} else {
		fmt.Println("\n── Step 3: Network ACL Bypass — skipped (--skip-network-acl) ────")
	}

	return state
}

// isGUID returns true if s looks like a UUID / GUID.
func isGUID(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}
