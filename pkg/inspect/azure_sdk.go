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
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
)

// azureResourceGraphQuery selects the managed databases KubeDB can host across
// every subscription the caller can read, with their sku and properties inline.
const azureResourceGraphQuery = `resources
| where type in~ (
    'microsoft.dbforpostgresql/flexibleservers',
    'microsoft.dbformysql/flexibleservers',
    'microsoft.cache/redis',
    'microsoft.documentdb/mongoclusters')
| project name, type, location, subscriptionId, sku, properties`

// discoverViaSDK inventories Azure databases through Azure Resource Graph using
// the default credential chain (env, managed identity, az login). A single KQL
// query returns every database across all readable subscriptions.
func (d azureDiscoverer) discoverViaSDK(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("azure: credentials: %w", err)
	}
	client, err := armresourcegraph.NewClient(cred, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("azure: %w", err)
	}

	query := azureResourceGraphQuery
	format := armresourcegraph.ResultFormatObjectArray
	request := armresourcegraph.QueryRequest{
		Query:   &query,
		Options: &armresourcegraph.QueryRequestOptions{ResultFormat: &format},
	}
	if opts.Account != "" {
		sub := opts.Account
		request.Subscriptions = []*string{&sub}
	}

	var (
		out      []ManagedDatabase
		warnings []string
	)
	for {
		resp, err := client.Resources(ctx, request, nil)
		if err != nil {
			return out, warnings, fmt.Errorf("azure resource graph: %w", err)
		}
		rows, _ := resp.Data.([]any)
		for _, r := range rows {
			row, ok := r.(map[string]any)
			if !ok {
				continue
			}
			if db, w, ok := azureRowToDB(row, opts); ok {
				out = append(out, db)
				warnings = append(warnings, w...)
			}
		}
		if resp.SkipToken == nil || *resp.SkipToken == "" {
			break
		}
		request.Options.SkipToken = resp.SkipToken
	}
	return out, warnings, nil
}

func azureRowToDB(row map[string]any, opts Options) (ManagedDatabase, []string, bool) {
	typ := strings.ToLower(mapStr(row, "type"))
	name := mapStr(row, "name")
	location := mapStr(row, "location")
	account := mapStr(row, "subscriptionId")
	if opts.Account != "" {
		account = opts.Account
	}
	sku := mapObj(row, "sku")
	props := mapObj(row, "properties")

	switch {
	case strings.HasSuffix(typ, "/flexibleservers"):
		engine := "postgres"
		service := "Database for PostgreSQL"
		if strings.Contains(typ, "dbformysql") {
			engine, service = "mysql", "Database for MySQL"
		}
		skuName := mapStr(sku, "name")
		db := ManagedDatabase{
			Provider: ProviderAzure, Service: service, Engine: engine,
			Name: name, Account: account, Region: location, NodeType: skuName,
		}
		spec, ok := azureFlexSpec(skuName)
		if !ok {
			db.NodeCount, db.Notes = 1, "unknown SKU"
			return db, []string{fmt.Sprintf("azure %s %s: unknown SKU %q", service, name, skuName)}, true
		}
		db.VCPUPerNode, db.MemoryGiBPerNode, db.NodeCount = spec.VCPU, spec.MemoryGiB, 1
		if ha := mapObj(props, "highAvailability"); opts.CountStandby && ha != nil {
			if mode := mapStr(ha, "mode"); mode != "" && !strings.EqualFold(mode, "Disabled") {
				db.NodeCount = 2
			}
		}
		db.MonthlyCostUSD, db.CostEstimated = azureEstimateCost(db)
		return db, nil, true

	case strings.HasSuffix(typ, "/redis"):
		family := mapStr(sku, "family")
		capacity := int(mapNum(sku, "capacity"))
		db := ManagedDatabase{
			Provider: ProviderAzure, Service: "Cache for Redis", Engine: "redis",
			Name: name, Account: account, Region: location,
			NodeType: fmt.Sprintf("%s %s%d", mapStr(sku, "name"), family, capacity),
		}
		memGiB, ok := azureRedisGiB(family, capacity)
		if !ok {
			db.NodeCount, db.Notes = 1, "unknown Redis SKU"
			return db, []string{fmt.Sprintf("azure redis %s: unknown SKU %s%d", name, family, capacity)}, true
		}
		shards := atLeastOne(int(mapNum(props, "shardCount")))
		replicas := int(mapNum(props, "replicasPerMaster"))
		if replicas == 0 && opts.CountStandby && !strings.EqualFold(mapStr(sku, "name"), "Basic") {
			replicas = 1
		}
		db.MemoryGiBPerNode = memGiB
		db.NodeCount = shards * (1 + replicas)
		db.MonthlyCostUSD, db.CostEstimated = azureEstimateCost(db)
		return db, nil, true

	case strings.HasSuffix(typ, "/mongoclusters"):
		groups, _ := props["nodeGroupSpecs"].([]any)
		if len(groups) == 0 {
			return ManagedDatabase{}, nil, false
		}
		ng := mapObjAny(groups[0])
		skuName := mapStr(ng, "sku")
		db := ManagedDatabase{
			Provider: ProviderAzure, Service: "Cosmos DB for MongoDB vCore", Engine: "mongodb",
			Name: name, Account: account, Region: location, NodeType: skuName,
		}
		spec, ok := azureMongoVCoreSpec[strings.ToUpper(skuName)]
		if !ok {
			db.NodeCount, db.Notes = atLeastOne(int(mapNum(ng, "nodeCount"))), "unknown vCore tier"
			return db, []string{fmt.Sprintf("azure mongo-vcore %s: unknown tier %q", name, skuName)}, true
		}
		shards := atLeastOne(int(mapNum(ng, "nodeCount")))
		perShard := 1
		if b, _ := ng["enableHa"].(bool); b && opts.CountStandby {
			perShard = 2
		}
		db.VCPUPerNode, db.MemoryGiBPerNode = spec.VCPU, spec.MemoryGiB
		db.NodeCount = shards * perShard
		db.MonthlyCostUSD, db.CostEstimated = azureEstimateCost(db)
		return db, nil, true
	}
	return ManagedDatabase{}, nil, false
}

// map navigation helpers for Resource Graph's dynamic rows.
func mapStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func mapNum(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	switch n := m[key].(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}

func mapObj(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	o, _ := m[key].(map[string]any)
	return o
}

func mapObjAny(v any) map[string]any {
	o, _ := v.(map[string]any)
	return o
}
