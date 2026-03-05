package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// buildCredential returns an azcore.TokenCredential for the caller.
//
// Priority (first match wins):
//  1. --managed-identity-client-id → ManagedIdentityCredential
//     For unattended use on Azure VMs, Container Apps, Functions, etc.
//  2. --interactive → InteractiveBrowserCredential
//     Opens a browser tab for interactive sign-in.
//  3. (default) → DefaultAzureCredential
//     Automatically picks up credentials from (in order): environment
//     variables, workload identity, managed identity, Azure CLI,
//     Azure Developer CLI, Azure PowerShell, VS Code.
func buildCredential(tenant, clientID, managedIdentityClientID string, interactive bool) azcore.TokenCredential {
	if managedIdentityClientID != "" {
		opts := &azidentity.ManagedIdentityCredentialOptions{
			ID: azidentity.ClientID(managedIdentityClientID),
		}
		cred, err := azidentity.NewManagedIdentityCredential(opts)
		if err != nil {
			log.Fatalf("ManagedIdentityCredential: %v", err)
		}
		return cred
	}

	if interactive {
		opts := &azidentity.InteractiveBrowserCredentialOptions{}
		if tenant != "" {
			opts.TenantID = tenant
		}
		if clientID != "" {
			opts.ClientID = clientID
		}
		cred, err := azidentity.NewInteractiveBrowserCredential(opts)
		if err != nil {
			log.Fatalf("InteractiveBrowserCredential: %v", err)
		}
		return cred
	}

	// Default: uses env vars → workload identity → managed identity →
	// Azure CLI → azd → Azure PowerShell → VS Code
	dopts := &azidentity.DefaultAzureCredentialOptions{}
	if tenant != "" {
		dopts.TenantID = tenant
	}
	cred, err := azidentity.NewDefaultAzureCredential(dopts)
	if err != nil {
		log.Fatalf("DefaultAzureCredential: %v", err)
	}
	return cred
}

// acquireToken requests an access token for the Fabric API scope using the
// given credential.
func acquireToken(ctx context.Context, cred azcore.TokenCredential) string {
	return acquireTokenForScope(ctx, cred, fabricScope)
}

// acquireTokenForScope requests an access token for an arbitrary scope.
// This is used when different API surfaces require different audiences
// (e.g. Fabric vs Cosmos DB vs Power BI).
func acquireTokenForScope(ctx context.Context, cred azcore.TokenCredential, scope string) string {
	tk, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{scope},
	})
	if err != nil {
		log.Fatalf("GetToken(%s): %v", scope, err)
	}
	return tk.Token
}

// ---------------------------------------------------------------------------
// JWT claim helpers (ARM token introspection)
// ---------------------------------------------------------------------------

// getTokenClaims acquires a token for the given scope and parses its JWT
// payload to return the claims map.
func getTokenClaims(ctx context.Context, cred azcore.TokenCredential, scope string) (map[string]any, error) {
	tk, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{scope},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to acquire token for %s: %w", scope, err)
	}

	parts := strings.Split(tk.Token, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid JWT format")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("failed to decode JWT payload: %w", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("failed to parse JWT claims: %w", err)
	}
	return claims, nil
}

// getPrincipalObjectID extracts the current user/service principal object ID
// from an ARM access token (the "oid" claim).
func getPrincipalObjectID(ctx context.Context, cred azcore.TokenCredential) (string, error) {
	claims, err := getTokenClaims(ctx, cred, armScope)
	if err != nil {
		return "", err
	}
	if oid, ok := claims["oid"].(string); ok && strings.TrimSpace(oid) != "" {
		return strings.TrimSpace(oid), nil
	}
	return "", fmt.Errorf("JWT token does not contain 'oid' claim")
}

// getTenantID extracts the tenant ID from an ARM access token (the "tid"
// claim).
func getTenantID(ctx context.Context, cred azcore.TokenCredential) (string, error) {
	claims, err := getTokenClaims(ctx, cred, armScope)
	if err != nil {
		return "", err
	}
	if tid, ok := claims["tid"].(string); ok && strings.TrimSpace(tid) != "" {
		return strings.TrimSpace(tid), nil
	}
	return "", fmt.Errorf("JWT token does not contain 'tid' claim")
}
