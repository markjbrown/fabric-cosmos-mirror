package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos"
)

// ---------------------------------------------------------------------------
// Service tag types
// ---------------------------------------------------------------------------

// ServiceTagFile represents the top-level Azure service-tags JSON.
type ServiceTagFile struct {
	ChangeNumber int               `json:"changeNumber"`
	Cloud        string            `json:"cloud"`
	Values       []ServiceTagValue `json:"values"`
}

// ServiceTagValue is a single service-tag entry.
type ServiceTagValue struct {
	Name       string               `json:"name"`
	ID         string               `json:"id"`
	Properties ServiceTagProperties `json:"properties"`
}

// ServiceTagProperties holds the IP prefixes and metadata for a tag.
type ServiceTagProperties struct {
	ChangeNumber    int      `json:"changeNumber"`
	Region          string   `json:"region"`
	SystemService   string   `json:"systemService"`
	AddressPrefixes []string `json:"addressPrefixes"`
}

// ---------------------------------------------------------------------------
// Fetch service tags via ARM REST API
// ---------------------------------------------------------------------------

// fetchServiceTags retrieves Azure service tags using the ARM Service Tags API.
// See: https://learn.microsoft.com/en-us/rest/api/virtualnetwork/service-tags/list
func fetchServiceTags(ctx context.Context, cred azcore.TokenCredential, subscriptionID, location string) *ServiceTagFile {
	// Acquire ARM token
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{armScope},
	})
	if err != nil {
		log.Fatalf("Failed to acquire ARM token for service tags: %v", err)
	}

	// Call Service Tags - List API
	apiURL := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/providers/Microsoft.Network/locations/%s/serviceTags?api-version=2024-05-01",
		subscriptionID, location)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		log.Fatalf("Failed to build service-tags request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("Failed to fetch service tags: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		log.Fatalf("Service Tags API returned %d: %s", resp.StatusCode, string(body))
	}

	var tags ServiceTagFile
	if err := json.Unmarshal(body, &tags); err != nil {
		log.Fatalf("Failed to parse service-tags response: %v", err)
	}

	fmt.Printf("Loaded %d service-tag entries (cloud: %s) from ARM API\n", len(tags.Values), tags.Cloud)
	return &tags
}

// ---------------------------------------------------------------------------
// IP extraction
// ---------------------------------------------------------------------------

// extractFabricIPs returns the IPv4 prefixes required for Fabric mirroring:
//
//   - DataFactory.<region> — regional IPs for the Cosmos DB account's location.
//   - PowerQueryOnline     — the GLOBAL list of IPv4 IPs. PowerQuery does not
//     run in every Azure region, so regional filtering is not possible; the
//     full set of PowerQueryOnline addresses must be allowed.
//
// These IPs are added to the Cosmos DB account's IP firewall temporarily
// during mirroring setup. After the mirror is created and replication has
// started, the original firewall rules are restored (unless --no-restore).
func extractFabricIPs(tags *ServiceTagFile, location string) []string {
	normalizedLoc := normalizeLocation(location)

	var ips []string
	var foundDataFactory bool

	for _, v := range tags.Values {
		// DataFactory — match the regional tag (IPv4 only)
		if strings.EqualFold(v.Properties.SystemService, "DataFactory") {
			tagRegion := normalizeLocation(v.Properties.Region)
			if tagRegion == normalizedLoc {
				var ipv4Count int
				for _, prefix := range v.Properties.AddressPrefixes {
					if !strings.Contains(prefix, ":") {
						ips = append(ips, prefix)
						ipv4Count++
					}
				}
				fmt.Printf("Found %s with %d IPv4 prefixes (of %d total)\n",
					v.Name, ipv4Count, len(v.Properties.AddressPrefixes))
				foundDataFactory = true
			}
		}

		// PowerQueryOnline — global IPv4 addresses. PowerQuery does not
		// run in every region, so we must include the full global list.
		if strings.EqualFold(v.Name, "PowerQueryOnline") {
			var ipv4Count int
			for _, prefix := range v.Properties.AddressPrefixes {
				if !strings.Contains(prefix, ":") {
					ips = append(ips, prefix)
					ipv4Count++
				}
			}
			fmt.Printf("Found PowerQueryOnline with %d IPv4 prefixes\n", ipv4Count)
		}
	}

	if !foundDataFactory {
		fmt.Printf("WARNING: No DataFactory service tag found for region %q\n", location)
		fmt.Println("Available DataFactory regions:")
		for _, v := range tags.Values {
			if strings.EqualFold(v.Properties.SystemService, "DataFactory") && v.Properties.Region != "" {
				fmt.Printf("  %s (region: %s)\n", v.Name, v.Properties.Region)
			}
		}
	}

	fmt.Printf("Total Fabric IP prefixes: %d\n", len(ips))
	return ips
}

// ---------------------------------------------------------------------------
// IP firewall configuration (Step 2)
// ---------------------------------------------------------------------------

// configureIPFirewall merges Fabric service-tag IPs with the account's
// existing IP rules and updates the Cosmos DB account.
func configureIPFirewall(ctx context.Context, cred azcore.TokenCredential, client *armcosmos.DatabaseAccountsClient,
	subscriptionID, rg, accountName, location string, state *AccountState) {

	fmt.Println("\n── Step 2: IP Firewall Configuration ────────────────────────────")

	// Fetch service tags via ARM API and extract IPs
	tags := fetchServiceTags(ctx, cred, subscriptionID, location)
	fabricIPs := extractFabricIPs(tags, location)
	if len(fabricIPs) == 0 {
		log.Fatal("No Fabric IP prefixes found — cannot configure firewall")
	}

	// Merge: existing IPs + Fabric IPs (deduplicated)
	ipSet := make(map[string]bool)
	for _, rule := range state.IPRules {
		if rule.IPAddressOrRange != nil {
			ipSet[*rule.IPAddressOrRange] = true
		}
	}
	for _, ip := range fabricIPs {
		ipSet[ip] = true
	}

	var mergedRules []*armcosmos.IPAddressOrRange
	for ip := range ipSet {
		mergedRules = append(mergedRules, &armcosmos.IPAddressOrRange{
			IPAddressOrRange: to.Ptr(ip),
		})
	}

	fmt.Printf("Existing IP rules: %d, Fabric IPs: %d, Merged total: %d\n",
		len(state.IPRules), len(fabricIPs), len(mergedRules))

	// Update account — enable public network access + merged IP rules
	params := armcosmos.DatabaseAccountUpdateParameters{
		Properties: &armcosmos.DatabaseAccountUpdateProperties{
			PublicNetworkAccess: to.Ptr(armcosmos.PublicNetworkAccessEnabled),
			IPRules:             mergedRules,
		},
	}

	fmt.Println("Updating Cosmos DB IP firewall (this may take several minutes)...")
	poller, err := client.BeginUpdate(ctx, rg, accountName, params, nil)
	if err != nil {
		log.Fatalf("Failed to begin IP firewall update: %v", err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		log.Fatalf("Failed to update IP firewall: %v", err)
	}
	fmt.Println("IP firewall configured successfully.")
}
