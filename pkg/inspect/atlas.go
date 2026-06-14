/*
Copyright AppsCode Inc. and Contributors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// AtlasCreds authenticates to the MongoDB Atlas Administration API. Service
// account OAuth2 (ClientID/ClientSecret) is the supported live method; API-key
// HTTP Digest is not implemented here (use a service account or --source=file).
type AtlasCreds struct {
	ClientID     string
	ClientSecret string
	OrgID        string
	BaseURL      string // defaults to https://cloud.mongodb.com
}

const atlasAPIVersion = "application/vnd.atlas.2025-03-12+json"

type atlasDiscoverer struct{}

func (atlasDiscoverer) Provider() Provider { return ProviderAtlas }

func (d atlasDiscoverer) Discover(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	switch opts.Source {
	case SourceSDK:
		return d.discoverViaSDK(ctx, opts)
	case SourceCLI:
		return nil, nil, fmt.Errorf("atlas: no CLI source; use --source=sdk, --source=rest or --source=file")
	case SourceFile:
		return discoverViaFile(opts.FromFile, atlasCollectors(), opts)
	case SourceREST:
		return d.discoverViaREST(ctx, opts)
	case SourceAuto:
		if opts.FromFile != "" {
			return discoverViaFile(opts.FromFile, atlasCollectors(), opts)
		}
		return d.discoverViaSDK(ctx, opts) // SDK is the preferred live source
	default:
		return nil, nil, fmt.Errorf("atlas: unsupported source %q", opts.Source)
	}
}

func atlasCollectors() []collector {
	return []collector{{key: "clusters", parse: parseAtlasClusters}}
}

func (d atlasDiscoverer) discoverViaREST(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	c := opts.Atlas
	if c.ClientID == "" || c.ClientSecret == "" {
		return nil, nil, fmt.Errorf("atlas: --atlas-client-id and --atlas-client-secret are required for --source=rest (or use --source=file)")
	}
	base := c.BaseURL
	if base == "" {
		base = "https://cloud.mongodb.com"
	}
	token, err := atlasToken(ctx, opts, base, c)
	if err != nil {
		return nil, nil, err
	}
	headers := map[string]string{"Authorization": "Bearer " + token, "Accept": atlasAPIVersion}

	projectIDs, err := atlasProjectIDs(ctx, opts, base, headers, c.OrgID)
	if err != nil {
		return nil, nil, err
	}

	var out []ManagedDatabase
	var warnings []string
	for _, pid := range projectIDs {
		var resp json.RawMessage
		if err := httpJSON(ctx, opts, "GET", base+"/api/atlas/v2/groups/"+pid+"/clusters", headers, nil, &resp); err != nil {
			warnings = append(warnings, fmt.Sprintf("atlas project %s: %v", pid, err))
			continue
		}
		dbs, warns, err := parseAtlasClusters(resp, "", opts)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("atlas project %s: %v", pid, err))
			continue
		}
		out = append(out, dbs...)
		warnings = append(warnings, warns...)
	}
	return out, warnings, nil
}

// atlasToken exchanges a service-account client id/secret for a bearer token.
func atlasToken(ctx context.Context, opts Options, base string, c AtlasCreds) (string, error) {
	form := strings.NewReader("grant_type=client_credentials")
	headers := map[string]string{
		"Authorization": "Basic " + basicAuth(c.ClientID, c.ClientSecret),
		"Content-Type":  "application/x-www-form-urlencoded",
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := httpJSON(ctx, opts, "POST", base+"/api/oauth/token", headers, form, &tok); err != nil {
		return "", fmt.Errorf("atlas oauth: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("atlas oauth: empty access token")
	}
	return tok.AccessToken, nil
}

func atlasProjectIDs(ctx context.Context, opts Options, base string, headers map[string]string, orgID string) ([]string, error) {
	listProjects := func(path string) ([]string, error) {
		var resp struct {
			Results []struct {
				ID string `json:"id"`
			} `json:"results"`
		}
		if err := httpJSON(ctx, opts, "GET", base+path, headers, nil, &resp); err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(resp.Results))
		for _, r := range resp.Results {
			ids = append(ids, r.ID)
		}
		return ids, nil
	}
	if orgID != "" {
		return listProjects("/api/atlas/v2/orgs/" + url.PathEscape(orgID) + "/groups")
	}
	return listProjects("/api/atlas/v2/groups")
}

// atlasClusterList is the cluster list response (advanced cluster shape).
type atlasClusterList struct {
	Results []atlasCluster `json:"results"`
}

type atlasCluster struct {
	Name             string `json:"name"`
	MongoDBVersion   string `json:"mongoDBVersion"`
	ReplicationSpecs []struct {
		RegionConfigs []struct {
			RegionName     string        `json:"regionName"`
			ProviderName   string        `json:"providerName"`
			ElectableSpecs atlasNodeSpec `json:"electableSpecs"`
			ReadOnlySpecs  atlasNodeSpec `json:"readOnlySpecs"`
			AnalyticsSpecs atlasNodeSpec `json:"analyticsSpecs"`
		} `json:"regionConfigs"`
	} `json:"replicationSpecs"`
}

type atlasNodeSpec struct {
	InstanceSize string `json:"instanceSize"`
	NodeCount    int    `json:"nodeCount"`
}

// parseAtlasClusters accepts either the {results:[...]} list response or a bare
// array of clusters.
func parseAtlasClusters(raw []byte, _ string, opts Options) ([]ManagedDatabase, []string, error) {
	var list atlasClusterList
	if err := json.Unmarshal(raw, &list); err != nil || list.Results == nil {
		var bare []atlasCluster
		if err2 := json.Unmarshal(raw, &bare); err2 != nil {
			if err != nil {
				return nil, nil, err
			}
			return nil, nil, err2
		}
		list.Results = bare
	}

	var out []ManagedDatabase
	var warnings []string
	for _, cl := range list.Results {
		var totalMem float64
		var totalNodes int
		var rep string
		var region string
		unknown := false
		for _, rs := range cl.ReplicationSpecs {
			for _, rc := range rs.RegionConfigs {
				if region == "" {
					region = rc.RegionName
				}
				for _, ns := range []atlasNodeSpec{rc.ElectableSpecs, rc.ReadOnlySpecs, rc.AnalyticsSpecs} {
					if ns.NodeCount == 0 || ns.InstanceSize == "" {
						continue
					}
					if rep == "" {
						rep = ns.InstanceSize
					}
					spec, ok := atlasSpec(ns.InstanceSize)
					if !ok {
						unknown = true
						warnings = append(warnings, fmt.Sprintf("atlas %s: unknown instance size %q", cl.Name, ns.InstanceSize))
						continue
					}
					totalMem += spec.MemoryGiB * float64(ns.NodeCount)
					totalNodes += ns.NodeCount
				}
			}
		}
		db := ManagedDatabase{
			Provider: ProviderAtlas,
			Service:  "Atlas",
			Engine:   "mongodb",
			Name:     cl.Name,
			Account:  opts.Atlas.OrgID,
			Region:   region,
			NodeType: rep,
		}
		if totalNodes == 0 {
			db.NodeCount = 1
			if unknown {
				db.Notes = "unknown instance size"
			}
			out = append(out, db)
			continue
		}
		db.NodeCount = totalNodes
		db.MemoryGiBPerNode = totalMem / float64(totalNodes)
		db.MonthlyCostUSD, db.CostEstimated = atlasEstimateCost(db)
		out = append(out, db)
	}
	return out, warnings, nil
}

// atlasRatePerGiBHour is a memory-normalized list-price anchor (dedicated tiers
// on AWS, ~ $0.066/GiB-hour). Estimate; override with actual spend.
const atlasRatePerGiBHour = 0.066

func atlasEstimateCost(db ManagedDatabase) (float64, bool) {
	if db.MemoryGiBPerNode <= 0 {
		return 0, false
	}
	return db.TotalMemoryGiB() * atlasRatePerGiBHour * hoursPerMonth, true
}
