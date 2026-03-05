//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

func main() {
	tenant := flag.String("tenant", "", "Entra tenant ID (optional)")
	flag.Parse()

	ctx := context.Background()

	opts := &azidentity.InteractiveBrowserCredentialOptions{}
	if *tenant != "" {
		opts.TenantID = *tenant
	}
	cred, err := azidentity.NewInteractiveBrowserCredential(opts)
	if err != nil {
		log.Fatal(err)
	}

	tk, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://api.fabric.microsoft.com/.default"},
	})
	if err != nil {
		log.Fatal(err)
	}

	url := "https://api.fabric.microsoft.com/v1/connections"
	count := 0

	for url != "" {
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		req.Header.Set("Authorization", "Bearer "+tk.Token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 {
			log.Fatalf("API returned %d: %s", resp.StatusCode, body)
		}

		var page struct {
			Value []struct {
				ID                string `json:"id"`
				DisplayName       string `json:"displayName"`
				ConnectivityType  string `json:"connectivityType"`
				ConnectionDetails struct {
					Type string `json:"type"`
					Path string `json:"path"`
				} `json:"connectionDetails"`
				CredentialDetails struct {
					CredentialType       string `json:"credentialType"`
					SingleSignOnType     string `json:"singleSignOnType"`
					ConnectionEncryption string `json:"connectionEncryption"`
				} `json:"credentialDetails"`
			} `json:"value"`
			ContinuationURI string `json:"continuationUri"`
		}
		json.Unmarshal(body, &page)

		for _, c := range page.Value {
			count++
			fmt.Printf("─────────────────────────────────────────────\n")
			fmt.Printf("  Name:             %s\n", c.DisplayName)
			fmt.Printf("  ID:               %s\n", c.ID)
			fmt.Printf("  Connection Type:  %s\n", c.ConnectionDetails.Type)
			fmt.Printf("  Path:             %s\n", c.ConnectionDetails.Path)
			fmt.Printf("  Connectivity:     %s\n", c.ConnectivityType)
			fmt.Printf("  Credential Type:  %s\n", c.CredentialDetails.CredentialType)
			fmt.Printf("  SSO Type:         %s\n", c.CredentialDetails.SingleSignOnType)
			fmt.Printf("  Encryption:       %s\n", c.CredentialDetails.ConnectionEncryption)
		}

		url = page.ContinuationURI
	}

	fmt.Printf("─────────────────────────────────────────────\n")
	fmt.Printf("\n%d connection(s) found\n", count)
}
