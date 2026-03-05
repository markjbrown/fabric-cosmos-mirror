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
	"strings"

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

	url := "https://api.fabric.microsoft.com/v1/connections/supportedConnectionTypes?showAllCreationMethods=true"

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
			Value           []json.RawMessage `json:"value"`
			ContinuationURI string            `json:"continuationUri"`
		}
		json.Unmarshal(body, &page)

		for _, raw := range page.Value {
			// Quick check if this entry contains "cosmos" (case-insensitive)
			if !strings.Contains(strings.ToLower(string(raw)), "cosmos") {
				continue
			}
			// Pretty-print the full JSON
			var obj interface{}
			json.Unmarshal(raw, &obj)
			pretty, _ := json.MarshalIndent(obj, "", "  ")
			fmt.Println(string(pretty))
			fmt.Println("---")
		}

		url = page.ContinuationURI
	}
}
