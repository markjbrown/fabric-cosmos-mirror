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
	filter := flag.String("filter", "", "Case-insensitive substring filter on connection type name (optional)")
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

	url := "https://api.fabric.microsoft.com/v1/connections/supportedConnectionTypes"
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
				Type            string   `json:"type"`
				CredentialTypes []string `json:"supportedCredentialTypes"`
			} `json:"value"`
			ContinuationURI string `json:"continuationUri"`
		}
		json.Unmarshal(body, &page)

		for _, ct := range page.Value {
			if *filter != "" && !strings.Contains(strings.ToLower(ct.Type), strings.ToLower(*filter)) {
				continue
			}
			count++
			fmt.Printf("%-50s  credentials: %s\n", ct.Type, strings.Join(ct.CredentialTypes, ", "))
		}

		url = page.ContinuationURI
	}

	fmt.Printf("\n%d connection type(s) listed\n", count)
}
