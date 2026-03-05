package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	fabricScope  = "https://api.fabric.microsoft.com/.default"
	cosmosScope  = "https://cosmos.azure.com/.default"
	powerbiScope = "https://analysis.windows.net/powerbi/api/.default"
	armScope     = "https://management.azure.com/.default"

	fabricBase  = "https://api.fabric.microsoft.com/v1"
	powerbiBase = "https://api.powerbi.com/v2.0/myorg"
)

// deriveAccountName extracts the Cosmos DB account name from an endpoint URL.
// e.g. "https://myaccount.documents.azure.com:443/" → "myaccount"
func deriveAccountName(endpoint string) string {
	host := endpoint
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	if i := strings.IndexAny(host, ":/"); i != -1 {
		host = host[:i]
	}
	if i := strings.Index(host, "."); i != -1 {
		return host[:i]
	}
	return host
}

// normalizeLocation converts an Azure location string to lowercase with no
// spaces, e.g. "West Central US" → "westcentralus".
func normalizeLocation(loc string) string {
	return strings.ToLower(strings.ReplaceAll(loc, " ", ""))
}

// ---------------------------------------------------------------------------
// Workspace lookup
// ---------------------------------------------------------------------------

// lookupWorkspace resolves a workspace display name to its ID via the
// Fabric REST API.
func lookupWorkspace(ctx context.Context, client *http.Client, token, name string) string {
	url := fabricBase + "/workspaces"
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("GET %s failed: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		log.Fatalf("List workspaces failed (%d): %s", resp.StatusCode, body)
	}

	var result struct {
		Value []struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"value"`
	}
	json.Unmarshal(body, &result)

	for _, ws := range result.Value {
		if strings.EqualFold(ws.DisplayName, name) {
			return ws.ID
		}
	}
	log.Fatalf("Workspace %q not found", name)
	return ""
}

// ---------------------------------------------------------------------------
// Connection lookup
// ---------------------------------------------------------------------------

// lookupConnection resolves a Fabric connection display name to its ID by
// listing all connections visible to the authenticated user.
func lookupConnection(ctx context.Context, client *http.Client, token, name string) string {
	url := fabricBase + "/connections"
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("GET %s failed: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		log.Fatalf("List connections failed (%d): %s", resp.StatusCode, body)
	}

	var result struct {
		Value []struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"value"`
	}
	json.Unmarshal(body, &result)

	for _, c := range result.Value {
		if strings.EqualFold(c.DisplayName, name) {
			return c.ID
		}
	}
	log.Fatalf("Connection %q not found", name)
	return ""
}

// ---------------------------------------------------------------------------
// Folder lookup
// ---------------------------------------------------------------------------

// lookupFolder resolves a folder display name to its ID within a workspace.
// Folders are not returned by the /items endpoint; the dedicated /folders
// endpoint must be used instead.
func lookupFolder(ctx context.Context, client *http.Client, token, workspaceID, name string) string {
	pageURL := fmt.Sprintf("%s/workspaces/%s/folders", fabricBase, workspaceID)

	for pageURL != "" {
		req, _ := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		if err != nil {
			log.Fatalf("GET %s failed: %v", pageURL, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Fatalf("List folders failed (%d): %s", resp.StatusCode, body)
		}

		var result struct {
			Value []struct {
				ID          string `json:"id"`
				DisplayName string `json:"displayName"`
			} `json:"value"`
			ContinuationURI string `json:"continuationUri"`
		}
		json.Unmarshal(body, &result)

		for _, f := range result.Value {
			if strings.EqualFold(f.DisplayName, name) {
				return f.ID
			}
		}

		pageURL = result.ContinuationURI
	}

	log.Fatalf("Folder %q not found in workspace %s", name, workspaceID)
	return ""
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

// doPost sends a JSON POST request with an Authorization header and returns
// the "id" field from the response body. If the response is a 202 (Accepted)
// with a Location header, it polls the long-running operation to completion
// and then extracts the item ID from the operation result.
func doPost(ctx context.Context, client *http.Client, token, url string, payload interface{}) string {
	data, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(data)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("POST %s failed: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var result map[string]interface{}
		json.Unmarshal(body, &result)
		if id, ok := result["id"].(string); ok {
			return id
		}
		log.Fatalf("No id in response: %s", body)

	case http.StatusAccepted:
		// Long-running operation — poll until complete
		location := resp.Header.Get("Location")
		if location == "" {
			log.Fatalf("202 Accepted but no Location header")
		}
		return waitForOperation(ctx, client, token, location)

	default:
		log.Fatalf("POST %s failed (%d): %s", url, resp.StatusCode, body)
	}
	return ""
}

// waitForOperation polls a Fabric long-running operation URL until the
// operation succeeds, then returns the resource ID from the result.
func waitForOperation(ctx context.Context, client *http.Client, token, location string) string {
	for {
		time.Sleep(5 * time.Second)

		req, _ := http.NewRequestWithContext(ctx, "GET", location, nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		if err != nil {
			log.Fatalf("GET %s failed: %v", location, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var status struct {
			Status           string `json:"status"`
			ResourceID       string `json:"resourceId"`
			ResourceLocation string `json:"resourceLocation"`
		}
		json.Unmarshal(body, &status)

		switch status.Status {
		case "Succeeded":
			if status.ResourceID != "" {
				return status.ResourceID
			}
			// Some operations return the ID via resourceLocation
			if status.ResourceLocation != "" {
				parts := strings.Split(status.ResourceLocation, "/")
				return parts[len(parts)-1]
			}
			log.Fatalf("Operation succeeded but no resource ID found: %s", body)
		case "Failed":
			log.Fatalf("Operation failed: %s", body)
		default:
			fmt.Printf("Operation status: %s, waiting...\n", status.Status)
		}
	}
}

// deepCopyMap creates a deep copy of a map[string]interface{} by
// round-tripping through JSON. Used when building payload variants.
func deepCopyMap(m map[string]interface{}) map[string]interface{} {
	data, _ := json.Marshal(m)
	var copy map[string]interface{}
	json.Unmarshal(data, &copy)
	return copy
}
