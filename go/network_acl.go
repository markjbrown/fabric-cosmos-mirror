package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos"
)

const fabricNetworkAclBypassCapability = "EnableFabricNetworkAclBypass"

// ---------------------------------------------------------------------------
// Network ACL Bypass (Step 3)
// ---------------------------------------------------------------------------

// configureNetworkACLBypass enables the Fabric Network ACL Bypass capability
// and adds the Fabric workspace resource ID to the bypass allowlist.
func configureNetworkACLBypass(ctx context.Context, client *armcosmos.DatabaseAccountsClient,
	rg, accountName, workspaceID, tenantID string, state *AccountState) {

	fmt.Println("\n── Step 3: Network ACL Bypass Configuration ─────────────────────")

	// 3a. Enable the EnableFabricNetworkAclBypass capability
	enableFabricCapability(ctx, client, rg, accountName, state)

	// 3b. Build the Fabric workspace resource ID
	// Format: /tenants/{tid}/subscriptions/00000000-.../resourceGroups/Fabric/providers/Microsoft.Fabric/workspaces/{wid}
	fabricResourceID := fmt.Sprintf(
		"/tenants/%s/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/Fabric/providers/Microsoft.Fabric/workspaces/%s",
		tenantID, workspaceID)
	fmt.Printf("Fabric resource ID: %s\n", fabricResourceID)

	// 3c. Merge with existing bypass resource IDs
	bypassSet := make(map[string]bool)
	for _, id := range state.NetworkACLBypassResourceIDs {
		if id != nil {
			bypassSet[*id] = true
		}
	}
	bypassSet[fabricResourceID] = true

	var mergedBypassIDs []*string
	for id := range bypassSet {
		mergedBypassIDs = append(mergedBypassIDs, to.Ptr(id))
	}

	fmt.Printf("Bypass resource IDs: %d (was %d)\n",
		len(mergedBypassIDs), len(state.NetworkACLBypassResourceIDs))

	// 3d. Update the account
	params := armcosmos.DatabaseAccountUpdateParameters{
		Properties: &armcosmos.DatabaseAccountUpdateProperties{
			NetworkACLBypass:            to.Ptr(armcosmos.NetworkACLBypassAzureServices),
			NetworkACLBypassResourceIDs: mergedBypassIDs,
		},
	}

	fmt.Println("Updating Network ACL Bypass (this may take several minutes)...")
	poller, err := client.BeginUpdate(ctx, rg, accountName, params, nil)
	if err != nil {
		log.Fatalf("Failed to begin Network ACL Bypass update: %v", err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		log.Fatalf("Failed to update Network ACL Bypass: %v", err)
	}
	fmt.Println("Network ACL Bypass configured successfully.")
}

// enableFabricCapability adds the EnableFabricNetworkAclBypass capability to
// the account if it is not already present.
func enableFabricCapability(ctx context.Context, client *armcosmos.DatabaseAccountsClient,
	rg, accountName string, state *AccountState) {

	// Already enabled?
	for _, cap := range state.Capabilities {
		if cap.Name != nil && strings.EqualFold(*cap.Name, fabricNetworkAclBypassCapability) {
			fmt.Printf("Capability %q already enabled.\n", fabricNetworkAclBypassCapability)
			return
		}
	}

	// Merge existing capabilities + new one
	caps := make([]*armcosmos.Capability, len(state.Capabilities))
	copy(caps, state.Capabilities)
	caps = append(caps, &armcosmos.Capability{
		Name: to.Ptr(fabricNetworkAclBypassCapability),
	})

	params := armcosmos.DatabaseAccountUpdateParameters{
		Properties: &armcosmos.DatabaseAccountUpdateProperties{
			Capabilities: caps,
		},
	}

	fmt.Printf("Enabling capability %q (this may take several minutes)...\n", fabricNetworkAclBypassCapability)
	poller, err := client.BeginUpdate(ctx, rg, accountName, params, nil)
	if err != nil {
		log.Fatalf("Failed to begin capability update: %v", err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		log.Fatalf("Failed to enable %q capability: %v", fabricNetworkAclBypassCapability, err)
	}
	fmt.Printf("Capability %q enabled.\n", fabricNetworkAclBypassCapability)
}
