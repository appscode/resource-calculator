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

// awsDiscoverer inventories AWS managed databases: RDS, Aurora, DocumentDB,
// Neptune (all via DescribeDBInstances), ElastiCache, MemoryDB and OpenSearch.
//
//   - SourceCLI shells out to the `aws` CLI per region (it carries the user's
//     profiles, SSO sessions and assume-role config).
//   - SourceFile parses a JSON bundle of the same `aws ... --output json` blobs.
type awsDiscoverer struct{}

func (awsDiscoverer) Provider() Provider { return ProviderAWS }

func (d awsDiscoverer) Discover(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	switch opts.Source {
	case SourceSDK:
		return d.discoverViaSDK(ctx, opts)
	case SourceFile:
		return discoverViaFile(opts.FromFile, awsCollectors(), opts)
	case SourceCLI:
		if !cliAvailable("aws") {
			return nil, nil, fmt.Errorf("aws CLI not found on PATH; install it or use --source=file with exported JSON")
		}
		return d.discoverViaCLI(ctx, opts)
	case SourceAuto:
		if opts.FromFile != "" {
			return discoverViaFile(opts.FromFile, awsCollectors(), opts)
		}
		return d.discoverViaSDK(ctx, opts) // SDK is the preferred live source
	default:
		return nil, nil, fmt.Errorf("aws: unsupported source %q", opts.Source)
	}
}

func (d awsDiscoverer) discoverViaCLI(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	regions := opts.Regions
	if len(regions) == 0 {
		if opts.AllRegions {
			rs, err := awsEnabledRegions(ctx, opts)
			if err != nil {
				return nil, nil, err
			}
			regions = rs
		} else {
			regions = []string{""} // CLI default region
		}
	}

	var (
		out      []ManagedDatabase
		warnings []string
	)
	for _, region := range regions {
		for _, c := range awsCollectors() {
			args := append([]string{}, c.args...)
			if region != "" {
				args = append(args, "--region", region)
			}
			if opts.Account != "" {
				args = append(args, "--profile", opts.Account)
			}
			raw, err := runCLIJSON(ctx, opts, "aws", args...)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s (%s): %v", c.key, region, err))
				continue
			}
			dbs, warns, err := c.parse(raw, region, opts)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s (%s): %v", c.key, region, err))
				continue
			}
			out = append(out, dbs...)
			warnings = append(warnings, warns...)
		}
		// OpenSearch needs a list-then-describe round trip.
		dbs, warns := awsDiscoverOpenSearchCLI(ctx, opts, region)
		out = append(out, dbs...)
		warnings = append(warnings, warns...)
	}
	if opts.Org {
		warnings = append(warnings, "aws: --org organization-wide scan requires per-account role assumption; scan each member account with --account=<profile> (see docs/compare.md)")
	}
	return out, warnings, nil
}

func awsEnabledRegions(ctx context.Context, opts Options) ([]string, error) {
	args := []string{"ec2", "describe-regions", "--output", "json", "--query", "Regions[?OptInStatus!=`not-opted-in`]"}
	if opts.Account != "" {
		args = append(args, "--profile", opts.Account)
	}
	raw, err := runCLIJSON(ctx, opts, "aws", args...)
	if err != nil {
		return nil, err
	}
	var regions []struct {
		RegionName string `json:"RegionName"`
	}
	if err := json.Unmarshal(raw, &regions); err != nil {
		return nil, fmt.Errorf("parse ec2 describe-regions: %w", err)
	}
	out := make([]string, 0, len(regions))
	for _, r := range regions {
		if r.RegionName != "" {
			out = append(out, r.RegionName)
		}
	}
	return out, nil
}

// awsCollectors are the CLI/file collectors shared by both discovery paths.
func awsCollectors() []collector {
	return []collector{
		{key: "rds-instances", args: []string{"rds", "describe-db-instances", "--output", "json"}, parse: parseAWSRDSInstances},
		{key: "elasticache-replication-groups", args: []string{"elasticache", "describe-replication-groups", "--output", "json"}, parse: parseAWSElastiCacheReplicationGroups},
		{key: "elasticache-cache-clusters", args: []string{"elasticache", "describe-cache-clusters", "--output", "json"}, parse: parseAWSElastiCacheClusters},
		{key: "memorydb-clusters", args: []string{"memorydb", "describe-clusters", "--show-shard-details", "--output", "json"}, parse: parseAWSMemoryDBClusters},
		{key: "opensearch-domains", args: nil, parse: parseAWSOpenSearchDomains},
	}
}

// ---------------------------------------------------------------------------
// RDS / Aurora / DocumentDB / Neptune (DescribeDBInstances)
// ---------------------------------------------------------------------------

type awsRDSInstances struct {
	DBInstances []struct {
		DBInstanceIdentifier string `json:"DBInstanceIdentifier"`
		DBInstanceClass      string `json:"DBInstanceClass"`
		Engine               string `json:"Engine"`
		DBInstanceStatus     string `json:"DBInstanceStatus"`
		MultiAZ              bool   `json:"MultiAZ"`
		DBClusterIdentifier  string `json:"DBClusterIdentifier"`
		AvailabilityZone     string `json:"AvailabilityZone"`
	} `json:"DBInstances"`
}

func parseAWSRDSInstances(raw []byte, region string, opts Options) ([]ManagedDatabase, []string, error) {
	var in awsRDSInstances
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, nil, err
	}
	var out []ManagedDatabase
	var warnings []string
	for _, i := range in.DBInstances {
		db := ManagedDatabase{
			Provider: ProviderAWS,
			Service:  awsServiceForEngine(i.Engine),
			Engine:   i.Engine,
			Name:     i.DBInstanceIdentifier,
			Account:  opts.Account,
			Region:   regionOr(region, i.AvailabilityZone),
			NodeType: i.DBInstanceClass,
		}
		if strings.EqualFold(i.DBInstanceClass, "db.serverless") {
			db.NodeCount = 1
			db.Notes = "serverless (ACU-based); memory not counted"
			warnings = append(warnings, fmt.Sprintf("rds %s: serverless instance excluded from memory total", i.DBInstanceIdentifier))
			out = append(out, db)
			continue
		}
		spec, ok := awsInstanceSpec(i.DBInstanceClass)
		if !ok {
			db.NodeCount = 1
			db.Notes = "unknown instance class"
			warnings = append(warnings, fmt.Sprintf("rds %s: unknown instance class %q", i.DBInstanceIdentifier, i.DBInstanceClass))
			out = append(out, db)
			continue
		}
		db.VCPUPerNode = spec.VCPU
		db.MemoryGiBPerNode = spec.MemoryGiB
		db.NodeCount = 1
		if i.MultiAZ && opts.CountStandby && i.DBClusterIdentifier == "" {
			db.NodeCount = 2 // hidden Multi-AZ standby, billed ~2x
		}
		db.MonthlyCostUSD, db.CostEstimated = awsEstimateCost(db)
		out = append(out, db)
	}
	return out, warnings, nil
}

func awsServiceForEngine(engine string) string {
	switch {
	case strings.HasPrefix(engine, "aurora"):
		return "Aurora"
	case engine == "docdb":
		return "DocumentDB"
	case engine == "neptune":
		return "Neptune"
	default:
		return "RDS"
	}
}

// ---------------------------------------------------------------------------
// ElastiCache
// ---------------------------------------------------------------------------

type awsElastiCacheReplicationGroups struct {
	ReplicationGroups []struct {
		ReplicationGroupID string   `json:"ReplicationGroupId"`
		CacheNodeType      string   `json:"CacheNodeType"`
		MemberClusters     []string `json:"MemberClusters"`
		Engine             string   `json:"Engine"`
		Status             string   `json:"Status"`
	} `json:"ReplicationGroups"`
}

func parseAWSElastiCacheReplicationGroups(raw []byte, region string, opts Options) ([]ManagedDatabase, []string, error) {
	var in awsElastiCacheReplicationGroups
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, nil, err
	}
	var out []ManagedDatabase
	var warnings []string
	for _, g := range in.ReplicationGroups {
		engine := g.Engine
		if engine == "" {
			engine = "redis"
		}
		db := ManagedDatabase{
			Provider: ProviderAWS,
			Service:  "ElastiCache",
			Engine:   engine,
			Name:     g.ReplicationGroupID,
			Account:  opts.Account,
			Region:   region,
			NodeType: g.CacheNodeType,
		}
		nodes := len(g.MemberClusters)
		if nodes == 0 {
			nodes = 1
		}
		spec, ok := awsInstanceSpec(g.CacheNodeType)
		if !ok {
			db.NodeCount = nodes
			db.Notes = "unknown node type"
			warnings = append(warnings, fmt.Sprintf("elasticache %s: unknown node type %q", g.ReplicationGroupID, g.CacheNodeType))
			out = append(out, db)
			continue
		}
		db.VCPUPerNode = spec.VCPU
		db.MemoryGiBPerNode = spec.MemoryGiB
		db.NodeCount = nodes
		db.MonthlyCostUSD, db.CostEstimated = awsEstimateCost(db)
		out = append(out, db)
	}
	return out, warnings, nil
}

type awsElastiCacheClusters struct {
	CacheClusters []struct {
		CacheClusterID     string `json:"CacheClusterId"`
		CacheNodeType      string `json:"CacheNodeType"`
		Engine             string `json:"Engine"`
		NumCacheNodes      int    `json:"NumCacheNodes"`
		ReplicationGroupID string `json:"ReplicationGroupId"`
	} `json:"CacheClusters"`
}

// parseAWSElastiCacheClusters handles Memcached and standalone clusters only;
// Redis/Valkey nodes are counted via replication groups to avoid double counting.
func parseAWSElastiCacheClusters(raw []byte, region string, opts Options) ([]ManagedDatabase, []string, error) {
	var in awsElastiCacheClusters
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, nil, err
	}
	var out []ManagedDatabase
	var warnings []string
	for _, c := range in.CacheClusters {
		if c.ReplicationGroupID != "" {
			continue // part of a replication group, already counted
		}
		db := ManagedDatabase{
			Provider: ProviderAWS,
			Service:  "ElastiCache",
			Engine:   c.Engine,
			Name:     c.CacheClusterID,
			Account:  opts.Account,
			Region:   region,
			NodeType: c.CacheNodeType,
		}
		nodes := c.NumCacheNodes
		if nodes == 0 {
			nodes = 1
		}
		spec, ok := awsInstanceSpec(c.CacheNodeType)
		if !ok {
			db.NodeCount = nodes
			db.Notes = "unknown node type"
			warnings = append(warnings, fmt.Sprintf("elasticache %s: unknown node type %q", c.CacheClusterID, c.CacheNodeType))
			out = append(out, db)
			continue
		}
		db.VCPUPerNode = spec.VCPU
		db.MemoryGiBPerNode = spec.MemoryGiB
		db.NodeCount = nodes
		db.MonthlyCostUSD, db.CostEstimated = awsEstimateCost(db)
		out = append(out, db)
	}
	return out, warnings, nil
}

// ---------------------------------------------------------------------------
// MemoryDB
// ---------------------------------------------------------------------------

type awsMemoryDBClusters struct {
	Clusters []struct {
		Name           string `json:"Name"`
		NodeType       string `json:"NodeType"`
		NumberOfShards int    `json:"NumberOfShards"`
		Engine         string `json:"Engine"`
		Shards         []struct {
			NumberOfNodes int `json:"NumberOfNodes"`
		} `json:"Shards"`
	} `json:"Clusters"`
}

func parseAWSMemoryDBClusters(raw []byte, region string, opts Options) ([]ManagedDatabase, []string, error) {
	var in awsMemoryDBClusters
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, nil, err
	}
	var out []ManagedDatabase
	var warnings []string
	for _, c := range in.Clusters {
		engine := c.Engine
		if engine == "" {
			engine = "redis"
		}
		nodes := 0
		for _, s := range c.Shards {
			nodes += s.NumberOfNodes
		}
		if nodes == 0 {
			nodes = c.NumberOfShards // fall back when shard details absent
		}
		if nodes == 0 {
			nodes = 1
		}
		db := ManagedDatabase{
			Provider: ProviderAWS,
			Service:  "MemoryDB",
			Engine:   engine,
			Name:     c.Name,
			Account:  opts.Account,
			Region:   region,
			NodeType: c.NodeType,
		}
		spec, ok := awsInstanceSpec(c.NodeType)
		if !ok {
			db.NodeCount = nodes
			db.Notes = "unknown node type"
			warnings = append(warnings, fmt.Sprintf("memorydb %s: unknown node type %q", c.Name, c.NodeType))
			out = append(out, db)
			continue
		}
		db.VCPUPerNode = spec.VCPU
		db.MemoryGiBPerNode = spec.MemoryGiB
		db.NodeCount = nodes
		db.MonthlyCostUSD, db.CostEstimated = awsEstimateCost(db)
		out = append(out, db)
	}
	return out, warnings, nil
}

// ---------------------------------------------------------------------------
// OpenSearch Service
// ---------------------------------------------------------------------------

func awsDiscoverOpenSearchCLI(ctx context.Context, opts Options, region string) ([]ManagedDatabase, []string) {
	base := []string{"opensearch", "list-domain-names", "--output", "json"}
	if region != "" {
		base = append(base, "--region", region)
	}
	if opts.Account != "" {
		base = append(base, "--profile", opts.Account)
	}
	raw, err := runCLIJSON(ctx, opts, "aws", base...)
	if err != nil {
		return nil, []string{fmt.Sprintf("opensearch list-domain-names (%s): %v", region, err)}
	}
	var names struct {
		DomainNames []struct {
			DomainName string `json:"DomainName"`
		} `json:"DomainNames"`
	}
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, []string{fmt.Sprintf("opensearch list-domain-names (%s): %v", region, err)}
	}
	if len(names.DomainNames) == 0 {
		return nil, nil
	}
	args := []string{"opensearch", "describe-domains", "--output", "json", "--domain-names"}
	for _, n := range names.DomainNames {
		args = append(args, n.DomainName)
	}
	if region != "" {
		args = append(args, "--region", region)
	}
	if opts.Account != "" {
		args = append(args, "--profile", opts.Account)
	}
	desc, err := runCLIJSON(ctx, opts, "aws", args...)
	if err != nil {
		return nil, []string{fmt.Sprintf("opensearch describe-domains (%s): %v", region, err)}
	}
	dbs, warns, err := parseAWSOpenSearchDomains(desc, region, opts)
	if err != nil {
		return nil, []string{fmt.Sprintf("opensearch describe-domains (%s): %v", region, err)}
	}
	return dbs, warns
}

type awsOpenSearchDomains struct {
	DomainStatusList []struct {
		DomainName    string `json:"DomainName"`
		EngineVersion string `json:"EngineVersion"`
		ClusterConfig struct {
			InstanceType           string `json:"InstanceType"`
			InstanceCount          int    `json:"InstanceCount"`
			DedicatedMasterEnabled bool   `json:"DedicatedMasterEnabled"`
			DedicatedMasterType    string `json:"DedicatedMasterType"`
			DedicatedMasterCount   int    `json:"DedicatedMasterCount"`
			WarmEnabled            bool   `json:"WarmEnabled"`
			WarmType               string `json:"WarmType"`
			WarmCount              int    `json:"WarmCount"`
		} `json:"ClusterConfig"`
	} `json:"DomainStatusList"`
}

func parseAWSOpenSearchDomains(raw []byte, region string, opts Options) ([]ManagedDatabase, []string, error) {
	var in awsOpenSearchDomains
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, nil, err
	}
	var out []ManagedDatabase
	var warnings []string
	for _, dom := range in.DomainStatusList {
		cc := dom.ClusterConfig
		db := ManagedDatabase{
			Provider: ProviderAWS,
			Service:  "OpenSearch",
			Engine:   "opensearch",
			Name:     dom.DomainName,
			Account:  opts.Account,
			Region:   region,
			NodeType: cc.InstanceType,
		}
		spec, ok := awsInstanceSpec(cc.InstanceType)
		if !ok {
			db.NodeCount = atLeastOne(cc.InstanceCount)
			db.Notes = "unknown instance type"
			warnings = append(warnings, fmt.Sprintf("opensearch %s: unknown instance type %q", dom.DomainName, cc.InstanceType))
			out = append(out, db)
			continue
		}
		dataNodes := atLeastOne(cc.InstanceCount)
		db.VCPUPerNode = spec.VCPU
		db.MemoryGiBPerNode = spec.MemoryGiB
		db.NodeCount = dataNodes

		// Dedicated masters and warm nodes add memory; include them only when
		// requested (they are infrastructure, not data servers).
		if opts.IncludeNonData && cc.DedicatedMasterEnabled && cc.DedicatedMasterCount > 0 {
			if ms, ok := awsInstanceSpec(cc.DedicatedMasterType); ok {
				out = append(out, ManagedDatabase{
					Provider: ProviderAWS, Service: "OpenSearch", Engine: "opensearch",
					Name: dom.DomainName + " (masters)", Account: opts.Account, Region: region,
					NodeType: cc.DedicatedMasterType, VCPUPerNode: ms.VCPU, MemoryGiBPerNode: ms.MemoryGiB,
					NodeCount: cc.DedicatedMasterCount,
				})
			}
		}
		db.MonthlyCostUSD, db.CostEstimated = awsEstimateCost(db)
		out = append(out, db)
	}
	return out, warnings, nil
}

// ---------------------------------------------------------------------------
// Pricing (memory-normalized list-price estimate, USD/GiB-hour by service).
// These anchors come from us-east-1 on-demand rates and are intentionally
// memory-normalized so the comparison aligns with the KubeDB memory metric.
// They are estimates; supply actual costs to override.
// ---------------------------------------------------------------------------

var awsServiceRatePerGiBHour = map[string]float64{
	"RDS":         0.0145,
	"Aurora":      0.0160,
	"DocumentDB":  0.0150,
	"Neptune":     0.0150,
	"ElastiCache": 0.0128,
	"MemoryDB":    0.0157,
	"OpenSearch":  0.0104,
}

func awsEstimateCost(db ManagedDatabase) (float64, bool) {
	rate, ok := awsServiceRatePerGiBHour[db.Service]
	if !ok || db.MemoryGiBPerNode <= 0 {
		return 0, false
	}
	return db.TotalMemoryGiB() * rate * hoursPerMonth, true
}

func regionOr(region, az string) string {
	if region != "" {
		return region
	}
	// derive region from an AZ like "us-east-1a" -> "us-east-1"
	if n := len(az); n > 1 {
		last := az[n-1]
		if last >= 'a' && last <= 'z' {
			return az[:n-1]
		}
	}
	return az
}

func atLeastOne(a int) int {
	if a < 1 {
		return 1
	}
	return a
}
