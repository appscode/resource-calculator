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

package compare

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ElasticCreds authenticates to the Elastic Cloud control-plane API.
type ElasticCreds struct {
	APIKey  string
	BaseURL string // defaults to https://api.elastic-cloud.com
}

type elasticDiscoverer struct{}

func (elasticDiscoverer) Provider() Provider { return ProviderElastic }

func (d elasticDiscoverer) Discover(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	switch opts.Source {
	case SourceSDK:
		return d.discoverViaSDK(opts)
	case SourceCLI:
		return nil, nil, fmt.Errorf("elastic: no CLI source; use --source=sdk, --source=rest or --source=file")
	case SourceFile:
		return discoverViaFile(opts.FromFile, elasticCollectors(), opts)
	case SourceREST:
		return d.discoverViaREST(ctx, opts)
	case SourceAuto:
		if opts.FromFile != "" {
			return discoverViaFile(opts.FromFile, elasticCollectors(), opts)
		}
		return d.discoverViaSDK(opts) // SDK is the preferred live source
	default:
		return nil, nil, fmt.Errorf("elastic: unsupported source %q", opts.Source)
	}
}

func elasticCollectors() []collector {
	return []collector{{key: "deployments", parse: parseElasticDeployments}}
}

func (d elasticDiscoverer) discoverViaREST(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	c := opts.Elastic
	if c.APIKey == "" {
		return nil, nil, fmt.Errorf("elastic: --elastic-api-key is required for --source=rest (or use --source=file)")
	}
	base := c.BaseURL
	if base == "" {
		base = "https://api.elastic-cloud.com"
	}
	headers := map[string]string{"Authorization": "ApiKey " + c.APIKey}

	var list struct {
		Deployments []struct {
			ID string `json:"id"`
		} `json:"deployments"`
	}
	if err := httpJSON(ctx, opts, "GET", base+"/api/v1/deployments", headers, nil, &list); err != nil {
		return nil, nil, err
	}

	var out []ManagedDatabase
	var warnings []string
	for _, dep := range list.Deployments {
		var detail json.RawMessage
		if err := httpJSON(ctx, opts, "GET", base+"/api/v1/deployments/"+dep.ID, headers, nil, &detail); err != nil {
			warnings = append(warnings, fmt.Sprintf("elastic deployment %s: %v", dep.ID, err))
			continue
		}
		dbs, err := parseElasticDeployment(detail, opts)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("elastic deployment %s: %v", dep.ID, err))
			continue
		}
		out = append(out, dbs...)
	}
	return out, warnings, nil
}

type elasticDeployment struct {
	Name      string `json:"name"`
	ID        string `json:"id"`
	Resources struct {
		Elasticsearch []struct {
			Region string `json:"region"`
			RefID  string `json:"ref_id"`
			Info   struct {
				PlanInfo struct {
					Current struct {
						Plan struct {
							ClusterTopology []elasticTopologyElement `json:"cluster_topology"`
						} `json:"plan"`
					} `json:"current"`
				} `json:"plan_info"`
			} `json:"info"`
		} `json:"elasticsearch"`
	} `json:"resources"`
}

type elasticTopologyElement struct {
	ZoneCount int      `json:"zone_count"`
	NodeRoles []string `json:"node_roles"`
	NodeType  struct {
		Data bool `json:"data"`
	} `json:"node_type"`
	Size struct {
		Value    int    `json:"value"` // MB per zone
		Resource string `json:"resource"`
	} `json:"size"`
}

// parseElasticDeployments handles the file-bundle form: an array of deployment
// detail objects, or {deployments:[...]} of detail objects.
func parseElasticDeployments(raw []byte, _ string, opts Options) ([]ManagedDatabase, []string, error) {
	var wrapper struct {
		Deployments []json.RawMessage `json:"deployments"`
	}
	var details []json.RawMessage
	if err := json.Unmarshal(raw, &wrapper); err == nil && wrapper.Deployments != nil {
		details = wrapper.Deployments
	} else {
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			return nil, nil, err
		}
		details = arr
	}
	var out []ManagedDatabase
	var warnings []string
	for _, d := range details {
		dbs, err := parseElasticDeployment(d, opts)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		out = append(out, dbs...)
	}
	return out, warnings, nil
}

func parseElasticDeployment(raw []byte, opts Options) ([]ManagedDatabase, error) {
	var dep elasticDeployment
	if err := json.Unmarshal(raw, &dep); err != nil {
		return nil, err
	}
	var out []ManagedDatabase
	for _, es := range dep.Resources.Elasticsearch {
		var totalMB int
		var nodes int
		region := es.Region
		for _, t := range es.Info.PlanInfo.Current.Plan.ClusterTopology {
			if t.Size.Value <= 0 {
				continue
			}
			if !opts.IncludeNonData && !elasticIsData(t) {
				continue
			}
			totalMB += t.Size.Value * atLeastOne(t.ZoneCount)
			nodes += atLeastOne(t.ZoneCount)
		}
		if nodes == 0 {
			continue
		}
		name := dep.Name
		if name == "" {
			name = dep.ID
		}
		memGiB := float64(totalMB) / 1024
		db := ManagedDatabase{
			Provider:         ProviderElastic,
			Service:          "Elasticsearch",
			Engine:           "elasticsearch",
			Name:             name,
			Region:           region,
			NodeType:         "memory-sized",
			MemoryGiBPerNode: memGiB / float64(nodes),
			NodeCount:        nodes,
		}
		db.MonthlyCostUSD, db.CostEstimated = elasticEstimateCost(db)
		out = append(out, db)
	}
	return out, nil
}

func elasticIsData(t elasticTopologyElement) bool {
	if len(t.NodeRoles) > 0 {
		for _, r := range t.NodeRoles {
			if strings.HasPrefix(r, "data") {
				return true
			}
		}
		return false
	}
	return t.NodeType.Data
}

// elasticRatePerGiBHour is Elastic Cloud's resource-based (per GB RAM/hour)
// pricing anchor. Estimate; override with actual spend.
const elasticRatePerGiBHour = 0.0559

func elasticEstimateCost(db ManagedDatabase) (float64, bool) {
	if db.MemoryGiBPerNode <= 0 {
		return 0, false
	}
	return db.TotalMemoryGiB() * elasticRatePerGiBHour * hoursPerMonth, true
}
