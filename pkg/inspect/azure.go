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
	"strings"
)

// azureDiscoverer inventories Azure managed databases via the `az` CLI (or a
// JSON bundle of the same `az ... list -o json` output). It focuses on the
// engines KubeDB can host: PostgreSQL/MySQL Flexible Server, Azure Cache for
// Redis and Cosmos DB for MongoDB (vCore). Cosmos DB (RU) and Azure SQL are
// throughput/abstracted and out of the memory-based comparison.
type azureDiscoverer struct{}

func (azureDiscoverer) Provider() Provider { return ProviderAzure }

func (d azureDiscoverer) Discover(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	switch opts.Source {
	case SourceSDK:
		return d.discoverViaSDK(ctx, opts)
	case SourceFile:
		return discoverViaFile(opts.FromFile, azureCollectors(), opts)
	case SourceCLI:
		if !cliAvailable("az") {
			return nil, nil, fmt.Errorf("az CLI not found on PATH; install it or use --source=file with exported JSON")
		}
		return d.discoverViaCLI(ctx, opts)
	case SourceAuto:
		if opts.FromFile != "" {
			return discoverViaFile(opts.FromFile, azureCollectors(), opts)
		}
		return d.discoverViaSDK(ctx, opts) // SDK is the preferred live source
	default:
		return nil, nil, fmt.Errorf("azure: unsupported source %q", opts.Source)
	}
}

func (d azureDiscoverer) discoverViaCLI(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	var (
		out      []ManagedDatabase
		warnings []string
	)
	for _, c := range azureCollectors() {
		args := append([]string{}, c.args...)
		if opts.Account != "" {
			args = append(args, "--subscription", opts.Account)
		}
		raw, err := runCLIJSON(ctx, opts, "az", args...)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", c.key, err))
			continue
		}
		dbs, warns, err := c.parse(raw, "", opts)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", c.key, err))
			continue
		}
		out = append(out, dbs...)
		warnings = append(warnings, warns...)
	}
	return out, warnings, nil
}

func azureCollectors() []collector {
	return []collector{
		{key: "postgres-flexible", args: []string{"postgres", "flexible-server", "list", "-o", "json"}, parse: parseAzurePostgres},
		{key: "mysql-flexible", args: []string{"mysql", "flexible-server", "list", "-o", "json"}, parse: parseAzureMySQL},
		{key: "redis", args: []string{"redis", "list", "-o", "json"}, parse: parseAzureRedis},
		{key: "mongo-vcore", args: []string{"cosmosdb", "mongocluster", "list", "-o", "json"}, parse: parseAzureMongoVCore},
	}
}

// azureFlexServer is the shared shape of a PostgreSQL/MySQL flexible server.
type azureFlexServer struct {
	Name     string `json:"name"`
	Location string `json:"location"`
	SKU      struct {
		Name string `json:"name"`
		Tier string `json:"tier"`
	} `json:"sku"`
	HighAvailability struct {
		Mode string `json:"mode"`
	} `json:"highAvailability"`
	ReplicationRole string `json:"replicationRole"`
}

func parseAzurePostgres(raw []byte, _ string, opts Options) ([]ManagedDatabase, []string, error) {
	return parseAzureFlex(raw, opts, "Database for PostgreSQL", "postgres")
}

func parseAzureMySQL(raw []byte, _ string, opts Options) ([]ManagedDatabase, []string, error) {
	return parseAzureFlex(raw, opts, "Database for MySQL", "mysql")
}

func parseAzureFlex(raw []byte, opts Options, service, engine string) ([]ManagedDatabase, []string, error) {
	var servers []azureFlexServer
	if err := json.Unmarshal(raw, &servers); err != nil {
		return nil, nil, err
	}
	var out []ManagedDatabase
	var warnings []string
	for _, s := range servers {
		db := ManagedDatabase{
			Provider: ProviderAzure,
			Service:  service,
			Engine:   engine,
			Name:     s.Name,
			Account:  opts.Account,
			Region:   s.Location,
			NodeType: s.SKU.Name,
		}
		spec, ok := azureFlexSpec(s.SKU.Name)
		if !ok {
			db.NodeCount = 1
			db.Notes = "unknown SKU"
			warnings = append(warnings, fmt.Sprintf("azure %s %s: unknown SKU %q", service, s.Name, s.SKU.Name))
			out = append(out, db)
			continue
		}
		db.VCPUPerNode = spec.VCPU
		db.MemoryGiBPerNode = spec.MemoryGiB
		db.NodeCount = 1
		if opts.CountStandby && s.HighAvailability.Mode != "" && !strings.EqualFold(s.HighAvailability.Mode, "Disabled") {
			db.NodeCount = 2 // zone-redundant / same-zone HA standby
		}
		db.MonthlyCostUSD, db.CostEstimated = azureEstimateCost(db)
		out = append(out, db)
	}
	return out, warnings, nil
}

type azureRedis struct {
	Name     string `json:"name"`
	Location string `json:"location"`
	SKU      struct {
		Name     string `json:"name"`
		Family   string `json:"family"`
		Capacity int    `json:"capacity"`
	} `json:"sku"`
	ShardCount        int `json:"shardCount"`
	ReplicasPerMaster int `json:"replicasPerMaster"`
}

func parseAzureRedis(raw []byte, _ string, opts Options) ([]ManagedDatabase, []string, error) {
	var items []azureRedis
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, nil, err
	}
	var out []ManagedDatabase
	var warnings []string
	for _, r := range items {
		db := ManagedDatabase{
			Provider: ProviderAzure,
			Service:  "Cache for Redis",
			Engine:   "redis",
			Name:     r.Name,
			Account:  opts.Account,
			Region:   r.Location,
			NodeType: fmt.Sprintf("%s %s%d", r.SKU.Name, r.SKU.Family, r.SKU.Capacity),
		}
		memGiB, ok := azureRedisGiB(r.SKU.Family, r.SKU.Capacity)
		if !ok {
			db.NodeCount = 1
			db.Notes = "unknown Redis SKU"
			warnings = append(warnings, fmt.Sprintf("azure redis %s: unknown SKU %s%d", r.Name, r.SKU.Family, r.SKU.Capacity))
			out = append(out, db)
			continue
		}
		shards := atLeastOne(r.ShardCount)
		replicas := r.ReplicasPerMaster
		if replicas == 0 && opts.CountStandby && !strings.EqualFold(r.SKU.Name, "Basic") {
			replicas = 1 // Standard/Premium include a replica
		}
		db.MemoryGiBPerNode = memGiB
		db.NodeCount = shards * (1 + replicas)
		db.MonthlyCostUSD, db.CostEstimated = azureEstimateCost(db)
		out = append(out, db)
	}
	return out, warnings, nil
}

// azureMongoVCoreSpec maps Cosmos DB for MongoDB vCore tiers to memory.
var azureMongoVCoreSpec = map[string]InstanceSpec{
	"M10": {1, 2}, "M20": {2, 4}, "M25": {2, 8}, "M30": {2, 8}, "M40": {4, 16},
	"M50": {8, 32}, "M60": {16, 64}, "M80": {32, 128}, "M200": {64, 256}, "M300": {80, 320},
}

type azureMongoCluster struct {
	Name       string `json:"name"`
	Location   string `json:"location"`
	Properties struct {
		NodeGroupSpecs []struct {
			SKU       string `json:"sku"`
			NodeCount int    `json:"nodeCount"`
			EnableHa  bool   `json:"enableHa"`
		} `json:"nodeGroupSpecs"`
	} `json:"properties"`
}

func parseAzureMongoVCore(raw []byte, _ string, opts Options) ([]ManagedDatabase, []string, error) {
	var items []azureMongoCluster
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, nil, err
	}
	var out []ManagedDatabase
	var warnings []string
	for _, c := range items {
		if len(c.Properties.NodeGroupSpecs) == 0 {
			continue
		}
		ng := c.Properties.NodeGroupSpecs[0]
		db := ManagedDatabase{
			Provider: ProviderAzure,
			Service:  "Cosmos DB for MongoDB vCore",
			Engine:   "mongodb",
			Name:     c.Name,
			Account:  opts.Account,
			Region:   c.Location,
			NodeType: ng.SKU,
		}
		spec, ok := azureMongoVCoreSpec[strings.ToUpper(ng.SKU)]
		if !ok {
			db.NodeCount = atLeastOne(ng.NodeCount)
			db.Notes = "unknown vCore tier"
			warnings = append(warnings, fmt.Sprintf("azure mongo-vcore %s: unknown tier %q", c.Name, ng.SKU))
			out = append(out, db)
			continue
		}
		shards := atLeastOne(ng.NodeCount)
		perShard := 1
		if ng.EnableHa && opts.CountStandby {
			perShard = 2
		}
		db.VCPUPerNode = spec.VCPU
		db.MemoryGiBPerNode = spec.MemoryGiB
		db.NodeCount = shards * perShard
		db.MonthlyCostUSD, db.CostEstimated = azureEstimateCost(db)
		out = append(out, db)
	}
	return out, warnings, nil
}

var azureServiceRatePerGiBHour = map[string]float64{
	"Database for PostgreSQL":     0.021,
	"Database for MySQL":          0.021,
	"Cache for Redis":             0.094,
	"Cosmos DB for MongoDB vCore": 0.025,
}

func azureEstimateCost(db ManagedDatabase) (float64, bool) {
	rate, ok := azureServiceRatePerGiBHour[db.Service]
	if !ok || db.MemoryGiBPerNode <= 0 {
		return 0, false
	}
	return db.TotalMemoryGiB() * rate * hoursPerMonth, true
}
