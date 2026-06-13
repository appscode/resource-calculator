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
	"fmt"
	"net/http"
	"strings"

	"github.com/elastic/cloud-sdk-go/pkg/api"
	"github.com/elastic/cloud-sdk-go/pkg/api/deploymentapi"
	"github.com/elastic/cloud-sdk-go/pkg/auth"
	"github.com/elastic/cloud-sdk-go/pkg/models"
)

// discoverViaSDK inventories Elastic Cloud deployments through the official
// cloud-sdk-go control-plane client (API key auth). The cloud-sdk-go calls are
// not context aware, so no context is threaded here.
func (d elasticDiscoverer) discoverViaSDK(opts Options) ([]ManagedDatabase, []string, error) {
	c := opts.Elastic
	if c.APIKey == "" {
		return nil, nil, fmt.Errorf("elastic: --elastic-api-key is required for --source=sdk (or use --source=file)")
	}
	key, err := auth.NewAPIKey(c.APIKey)
	if err != nil {
		return nil, nil, fmt.Errorf("elastic: %w", err)
	}
	host := c.BaseURL
	if host == "" {
		host = "https://api.elastic-cloud.com"
	}
	client, err := api.NewAPI(api.Config{Client: http.DefaultClient, AuthWriter: key, Host: host})
	if err != nil {
		return nil, nil, fmt.Errorf("elastic: %w", err)
	}

	list, err := deploymentapi.List(deploymentapi.ListParams{API: client})
	if err != nil {
		return nil, nil, fmt.Errorf("elastic list deployments: %w", err)
	}

	var (
		out      []ManagedDatabase
		warnings []string
	)
	for _, dep := range list.Deployments {
		if dep == nil || dep.ID == nil {
			continue
		}
		name := ""
		if dep.Name != nil {
			name = *dep.Name
		}
		got, err := deploymentapi.Get(deploymentapi.GetParams{API: client, DeploymentID: *dep.ID})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("elastic deployment %s: %v", *dep.ID, err))
			continue
		}
		out = append(out, elasticSDKDeployment(got, name, opts)...)
	}
	return out, warnings, nil
}

func elasticSDKDeployment(dep *models.DeploymentGetResponse, name string, opts Options) []ManagedDatabase {
	if dep == nil || dep.Resources == nil {
		return nil
	}
	if dep.Name != nil && *dep.Name != "" {
		name = *dep.Name
	}
	var out []ManagedDatabase
	for _, es := range dep.Resources.Elasticsearch {
		if es == nil || es.Info == nil || es.Info.PlanInfo == nil ||
			es.Info.PlanInfo.Current == nil || es.Info.PlanInfo.Current.Plan == nil {
			continue
		}
		region := ""
		if es.Region != nil {
			region = *es.Region
		}
		var totalMB, nodes int
		for _, t := range es.Info.PlanInfo.Current.Plan.ClusterTopology {
			if t == nil || t.Size == nil || t.Size.Value == nil || *t.Size.Value <= 0 {
				continue
			}
			if !opts.IncludeNonData && !elasticSDKIsData(t) {
				continue
			}
			zones := int(t.ZoneCount)
			if zones < 1 {
				zones = 1
			}
			totalMB += int(*t.Size.Value) * zones
			nodes += zones
		}
		if nodes == 0 {
			continue
		}
		memGiB := float64(totalMB) / 1024
		db := ManagedDatabase{
			Provider: ProviderElastic, Service: "Elasticsearch", Engine: "elasticsearch",
			Name: name, Region: region, NodeType: "memory-sized",
			MemoryGiBPerNode: memGiB / float64(nodes), NodeCount: nodes,
		}
		db.MonthlyCostUSD, db.CostEstimated = elasticEstimateCost(db)
		out = append(out, db)
	}
	return out
}

func elasticSDKIsData(t *models.ElasticsearchClusterTopologyElement) bool {
	if len(t.NodeRoles) > 0 {
		for _, r := range t.NodeRoles {
			if strings.HasPrefix(r, "data") {
				return true
			}
		}
		return false
	}
	return t.NodeType != nil && t.NodeType.Data != nil && *t.NodeType.Data
}
