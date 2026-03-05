package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	// Custom role created for Fabric mirroring — grants readMetadata
	// and readAnalytics at the account level.
	fabricMirroringRoleName = "FabricMirroringRole"

	// Built-in Cosmos SQL RBAC role: "Cosmos DB Built-in Data Contributor".
	builtInDataContributorID = "00000000-0000-0000-0000-000000000002"
)

// ---------------------------------------------------------------------------
// RBAC orchestration (Step 1)
// ---------------------------------------------------------------------------

// setupRBAC creates the custom FabricMirroringRole and assigns both the
// custom role and the built-in Data Contributor role to the given principal.
func setupRBAC(ctx context.Context, sqlClient *armcosmos.SQLResourcesClient,
	rg, accountName, accountScope, principalID string) {

	fmt.Println("\n── Step 1: RBAC Setup ────────────────────────────────────────────")

	// 1a. Create (or find) the custom FabricMirroringRole
	customRoleID := createFabricMirroringRole(ctx, sqlClient, rg, accountName, accountScope)

	// 1b. Assign the custom role to the principal
	fmt.Println("Assigning custom FabricMirroringRole...")
	assignCosmosRole(ctx, sqlClient, rg, accountName, customRoleID, principalID, accountScope)

	// 1c. Assign the built-in Data Contributor role
	builtInRoleFullID := fmt.Sprintf("%s/sqlRoleDefinitions/%s", accountScope, builtInDataContributorID)
	fmt.Println("Assigning built-in Data Contributor role...")
	assignCosmosRole(ctx, sqlClient, rg, accountName, builtInRoleFullID, principalID, accountScope)

	fmt.Println("RBAC setup complete.")
}

// ---------------------------------------------------------------------------
// Custom role definition
// ---------------------------------------------------------------------------

// createFabricMirroringRole creates a custom Cosmos SQL RBAC role with the
// readMetadata and readAnalytics data actions required for Fabric mirroring.
// If the role already exists (matched by name), its ID is returned.
func createFabricMirroringRole(ctx context.Context, sqlClient *armcosmos.SQLResourcesClient,
	rg, accountName, accountScope string) string {

	// Check for an existing role with the same name (idempotent)
	pager := sqlClient.NewListSQLRoleDefinitionsPager(rg, accountName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			log.Fatalf("Failed to list SQL role definitions: %v", err)
		}
		for _, rd := range page.Value {
			if rd.Properties != nil && rd.Properties.RoleName != nil &&
				*rd.Properties.RoleName == fabricMirroringRoleName {
				fmt.Printf("Custom role %q already exists: %s\n", fabricMirroringRoleName, *rd.ID)
				return *rd.ID
			}
		}
	}

	// Create the custom role
	roleDefType := armcosmos.RoleDefinitionTypeCustomRole
	roleDefID := uuid.NewSHA1(uuid.NameSpaceURL,
		[]byte(fabricMirroringRoleName+"|"+accountScope)).String()

	params := armcosmos.SQLRoleDefinitionCreateUpdateParameters{
		Properties: &armcosmos.SQLRoleDefinitionResource{
			RoleName:         to.Ptr(fabricMirroringRoleName),
			Type:             &roleDefType,
			AssignableScopes: []*string{to.Ptr(accountScope)},
			Permissions: []*armcosmos.Permission{{
				DataActions: []*string{
					to.Ptr("Microsoft.DocumentDB/databaseAccounts/readMetadata"),
					to.Ptr("Microsoft.DocumentDB/databaseAccounts/readAnalytics"),
				},
			}},
		},
	}

	fmt.Printf("Creating custom role %q...\n", fabricMirroringRoleName)
	poller, err := sqlClient.BeginCreateUpdateSQLRoleDefinition(ctx, roleDefID,
		rg, accountName, params, nil)
	if err != nil {
		log.Fatalf("Failed to create custom role definition: %v", err)
	}
	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		log.Fatalf("Failed to poll custom role definition creation: %v", err)
	}

	fmt.Printf("Created custom role: %s\n", *resp.ID)
	return *resp.ID
}

// ---------------------------------------------------------------------------
// Role assignment
// ---------------------------------------------------------------------------

// assignCosmosRole creates a Cosmos SQL RBAC role assignment idempotently.
// If a 409 Conflict is returned, the assignment already exists and is skipped.
func assignCosmosRole(ctx context.Context, sqlClient *armcosmos.SQLResourcesClient,
	rg, accountName, roleDefID, principalID, scope string) {

	// Deterministic assignment ID so reruns are idempotent
	assignmentID := uuid.NewSHA1(uuid.NameSpaceURL,
		[]byte(fmt.Sprintf("%s|%s|%s", scope, roleDefID, principalID))).String()

	params := armcosmos.SQLRoleAssignmentCreateUpdateParameters{
		Properties: &armcosmos.SQLRoleAssignmentResource{
			RoleDefinitionID: to.Ptr(roleDefID),
			PrincipalID:      to.Ptr(principalID),
			Scope:            to.Ptr(scope),
		},
	}

	poller, err := sqlClient.BeginCreateUpdateSQLRoleAssignment(ctx, assignmentID,
		rg, accountName, params, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == 409 {
			fmt.Println("  Role assignment already exists.")
			return
		}
		log.Fatalf("Failed to create role assignment: %v", err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		log.Fatalf("Failed to poll role assignment creation: %v", err)
	}
	fmt.Println("  Role assignment created/updated.")
}
