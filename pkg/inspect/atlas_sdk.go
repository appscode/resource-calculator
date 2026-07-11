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
	"fmt"

	admin "go.mongodb.org/atlas-sdk/v20250312019/admin"
)

// discoverViaSDK inventories MongoDB Atlas through the official Atlas SDK using
// service-account OAuth2 (client id/secret). It lists projects (org-wide when
// an org id is given), then the clusters in each project.
func (d atlasDiscoverer) discoverViaSDK(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	c := opts.Atlas
	if c.ClientID == "" || c.ClientSecret == "" {
		return nil, nil, fmt.Errorf("atlas: --atlas-client-id and --atlas-client-secret are required for --source=sdk (or use --source=file)")
	}
	mods := []admin.ClientModifier{admin.UseOAuthAuth(ctx, c.ClientID, c.ClientSecret)}
	if c.BaseURL != "" {
		mods = append(mods, admin.UseBaseURL(c.BaseURL))
	}
	client, err := admin.NewClient(mods...)
	if err != nil {
		return nil, nil, fmt.Errorf("atlas: %w", err)
	}

	var groups []admin.Group
	if c.OrgID != "" {
		resp, hr, err := client.OrganizationsApi.GetOrgGroups(ctx, c.OrgID).Execute()
		if err != nil {
			return nil, nil, fmt.Errorf("atlas list org projects: %w", err)
		}
		if hr != nil {
			_ = hr.Body.Close()
		}
		groups = resp.GetResults()
	} else {
		resp, hr, err := client.ProjectsApi.ListGroups(ctx).Execute()
		if err != nil {
			return nil, nil, fmt.Errorf("atlas list projects: %w", err)
		}
		if hr != nil {
			_ = hr.Body.Close()
		}
		groups = resp.GetResults()
	}

	var (
		out      []ManagedDatabase
		warnings []string
	)
	for i := range groups {
		gid := groups[i].GetId()
		resp, hr, err := client.ClustersApi.ListClusters(ctx, gid).Execute()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("atlas project %s: %v", gid, err))
			continue
		}
		if hr != nil {
			_ = hr.Body.Close()
		}
		for _, cl := range resp.GetResults() {
			db, warns := atlasSDKCluster(cl, c.OrgID, opts)
			out = append(out, db)
			warnings = append(warnings, warns...)
		}
	}
	return out, warnings, nil
}

func atlasSDKCluster(cl admin.ClusterDescription20240805, orgID string, _ Options) (ManagedDatabase, []string) {
	var (
		totalMem   float64
		totalNodes int
		rep        string
		region     string
		warnings   []string
	)
	add := func(size string, count int) {
		if count == 0 || size == "" {
			return
		}
		if rep == "" {
			rep = size
		}
		spec, ok := atlasSpec(size)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("atlas %s: unknown instance size %q", cl.GetName(), size))
			return
		}
		totalMem += spec.MemoryGiB * float64(count)
		totalNodes += count
	}
	for _, rs := range cl.GetReplicationSpecs() {
		for _, rc := range rs.GetRegionConfigs() {
			if region == "" {
				region = rc.GetRegionName()
			}
			es := rc.GetElectableSpecs()
			add(es.GetInstanceSize(), es.GetNodeCount())
			ro := rc.GetReadOnlySpecs()
			add(ro.GetInstanceSize(), ro.GetNodeCount())
			an := rc.GetAnalyticsSpecs()
			add(an.GetInstanceSize(), an.GetNodeCount())
		}
	}
	db := ManagedDatabase{
		Provider: ProviderAtlas, Service: "Atlas", Engine: "mongodb",
		Name: cl.GetName(), Account: orgID, Region: region, NodeType: rep,
	}
	if totalNodes == 0 {
		db.NodeCount = 1
		return db, warnings
	}
	db.NodeCount = totalNodes
	db.MemoryGiBPerNode = totalMem / float64(totalNodes)
	db.MonthlyCostUSD, db.CostEstimated = atlasEstimateCost(db)
	return db, warnings
}
