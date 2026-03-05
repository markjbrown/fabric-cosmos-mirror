//go:build future
// +build future

// ╔═══════════════════════════════════════════════════════════════════════╗
// ║  EXPERIMENTAL — OAuth2 Connection Creation via Power BI Gateway API  ║
// ║                                                                      ║
// ║  This file is excluded from normal builds (requires -tags future).   ║
// ║  The Fabric REST API's POST /v1/connections does NOT support OAuth2   ║
// ║  as a credential type. This code uses the undocumented Power BI      ║
// ║  gateway endpoint that the Fabric portal calls internally.           ║
// ║                                                                      ║
// ║  STATUS: Partially working. The API accepts a Cosmos access token    ║
// ║  but test-connection fails (firewall / credential format issues).    ║
// ║                                                                      ║
// ║  NEXT STEPS: Fabric team plans to support OAuth2 connection          ║
// ║  creation via Workspace Identity. See CONNECTION_CREATION.md.        ║
// ╚═══════════════════════════════════════════════════════════════════════╝

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// createCosmosConnection creates a cloud datasource for Azure Cosmos DB
// using the Power BI gateway API with OAuth2 credentials.
//
// This uses the undocumented v2.0 endpoint that the Fabric portal itself
// calls: POST https://api.powerbi.com/v2.0/myorg/me/gatewayClusterCloudDatasource
//
// It acquires a Cosmos DB-scoped access token using the caller's existing
// credential (no extra browser prompt) and sends it to the gateway API.
func createCosmosConnection(ctx context.Context, client *http.Client, cred azcore.TokenCredential, endpoint, displayName string) {
	// Get a Cosmos-scoped access token for the credential data (no new login)
	cosmosToken := acquireTokenForScope(ctx, cred, cosmosScope)
	// Get a Power BI-scoped token for the Authorization header (api.powerbi.com)
	pbiToken := acquireTokenForScope(ctx, cred, powerbiScope)

	connDetails, _ := json.Marshal(map[string]string{
		"host": endpoint,
	})

	// Build credentials with the access token
	credData := map[string]interface{}{
		"credentialData": []map[string]string{
			{"name": "accessToken", "value": cosmosToken},
		},
	}
	credJSON, _ := json.Marshal(credData)

	body := map[string]interface{}{
		"datasourceName":    displayName,
		"datasourceType":    "Extension",
		"connectionDetails": string(connDetails),
		"singleSignOnType":  "None",
		"mashupTestConnectionDetails": map[string]interface{}{
			"functionName":  "CosmosDB.Contents",
			"moduleName":    "CosmosDB",
			"moduleVersion": "1.0.7",
			"parameters": []map[string]interface{}{
				{
					"name":       "host",
					"type":       "text",
					"isRequired": true,
					"value":      endpoint,
				},
			},
		},
		"referenceDatasource": false,
		"credentialDetails": map[string]interface{}{
			"credentialType":      "OAuth2",
			"credentials":         string(credJSON),
			"encryptedConnection": "Any",
			"privacyLevel":        "Organizational",
			"skipTestConnection":  false,
			"encryptionAlgorithm": "NONE",
			"credentialSources":   []interface{}{},
			"skipGetOAuthToken":   true,
		},
		"allowDatasourceThroughGateway": false,
		"capabilities":                  0,
	}

	debugJSON, _ := json.MarshalIndent(body, "", "  ")
	fmt.Println("Connection payload:")
	fmt.Println(string(debugJSON))

	data, _ := json.Marshal(body)
	url := powerbiBase + "/me/gatewayClusterCloudDatasource"

	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+pbiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("POST %s failed: %v", url, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		log.Fatalf("Create cloud datasource failed (%d): %s", resp.StatusCode, respBody)
	}

	fmt.Printf("Cloud datasource created: %s\n", respBody)
}

// discoverCosmosDBType calls the ListSupportedConnectionTypes API and searches
// for the Cosmos DB connection type. It returns the exact type name, creation
// method, and parameter names that the API expects.
func discoverCosmosDBType(ctx context.Context, client *http.Client, token string) (connType, creationMethod string, paramNames []string) {
	url := fabricBase + "/connections/supportedConnectionTypes?showAllCreationMethods=true"

	type connInfo struct {
		Type            string `json:"type"`
		CreationMethods []struct {
			Name       string `json:"name"`
			Parameters []struct {
				Name     string `json:"name"`
				DataType string `json:"dataType"`
				Required bool   `json:"required"`
			} `json:"parameters"`
		} `json:"creationMethods"`
		SupportedCredentialTypes           []string `json:"supportedCredentialTypes"`
		SupportedConnectionEncryptionTypes []string `json:"supportedConnectionEncryptionTypes"`
		SupportsSkipTestConnection         bool     `json:"supportsSkipTestConnection"`
	}
	var matches []connInfo

	fmt.Println("Discovering Cosmos DB connection types...")
	for url != "" {
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		if err != nil {
			log.Fatalf("Failed to list supported connection types: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Fatalf("ListSupportedConnectionTypes failed (%d): %s", resp.StatusCode, body)
		}

		var page struct {
			Value           []connInfo `json:"value"`
			ContinuationURI string     `json:"continuationUri"`
		}
		json.Unmarshal(body, &page)

		for _, t := range page.Value {
			lower := strings.ToLower(t.Type)
			if strings.Contains(lower, "cosmos") {
				fmt.Printf("  Found Cosmos type: %s  credentials=%v  encryption=%v  skipTest=%v\n",
					t.Type, t.SupportedCredentialTypes, t.SupportedConnectionEncryptionTypes, t.SupportsSkipTestConnection)
				for _, cm := range t.CreationMethods {
					fmt.Printf("    Method: %s\n", cm.Name)
					for _, p := range cm.Parameters {
						fmt.Printf("      Param: name=%s dataType=%s required=%v\n", p.Name, p.DataType, p.Required)
					}
				}
				matches = append(matches, t)
			}
		}

		url = page.ContinuationURI
	}

	if len(matches) == 0 {
		log.Fatal("Could not find a Cosmos DB connection type in Fabric supported types")
	}

	for _, t := range matches {
		lower := strings.ToLower(t.Type)
		if strings.Contains(lower, "nosql") || t.Type == "AzureCosmosDB" {
			connType = t.Type
			if len(t.CreationMethods) > 0 {
				creationMethod = t.CreationMethods[0].Name
				for _, p := range t.CreationMethods[0].Parameters {
					paramNames = append(paramNames, p.Name)
				}
			}
			return
		}
	}

	for _, t := range matches {
		for _, ct := range t.SupportedCredentialTypes {
			if ct == "OAuth2" {
				connType = t.Type
				if len(t.CreationMethods) > 0 {
					creationMethod = t.CreationMethods[0].Name
					for _, p := range t.CreationMethods[0].Parameters {
						paramNames = append(paramNames, p.Name)
					}
				}
				return
			}
		}
	}

	t := matches[0]
	connType = t.Type
	if len(t.CreationMethods) > 0 {
		creationMethod = t.CreationMethods[0].Name
		for _, p := range t.CreationMethods[0].Parameters {
			paramNames = append(paramNames, p.Name)
		}
	}
	return
}
