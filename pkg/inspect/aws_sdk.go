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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// discoverViaSDK inventories AWS databases through aws-sdk-go-v2. It uses the
// default credential chain (env, shared config, SSO, instance role); --account
// selects a shared-config profile. With --org it enumerates member accounts via
// Organizations and assumes OrganizationAccountAccessRole in each.
func (d awsDiscoverer) discoverViaSDK(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	loadOpts := []func(*config.LoadOptions) error{}
	if opts.Account != "" {
		loadOpts = append(loadOpts, config.WithSharedConfigProfile(opts.Account))
	}
	if len(opts.Regions) == 1 {
		loadOpts = append(loadOpts, config.WithRegion(opts.Regions[0]))
	}
	baseCfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("aws: load config: %w", err)
	}

	var (
		out      []ManagedDatabase
		warnings []string
	)

	if opts.Org {
		accounts, err := awsListAccounts(ctx, baseCfg)
		if err != nil {
			return nil, warnings, err
		}
		stsClient := sts.NewFromConfig(baseCfg)
		for _, acct := range accounts {
			roleArn := fmt.Sprintf("arn:aws:iam::%s:role/OrganizationAccountAccessRole", acct)
			acctCfg := baseCfg.Copy()
			acctCfg.Credentials = aws.NewCredentialsCache(
				stscreds.NewAssumeRoleProvider(stsClient, roleArn))
			dbs, warns := awsScanAccount(ctx, acctCfg, acct, opts)
			out = append(out, dbs...)
			warnings = append(warnings, warns...)
		}
		return out, warnings, nil
	}

	dbs, warns := awsScanAccount(ctx, baseCfg, opts.Account, opts)
	return append(out, dbs...), append(warnings, warns...), nil
}

func awsListAccounts(ctx context.Context, cfg aws.Config) ([]string, error) {
	client := organizations.NewFromConfig(cfg)
	var ids []string
	p := organizations.NewListAccountsPaginator(client, &organizations.ListAccountsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return ids, fmt.Errorf("aws organizations: %w", err)
		}
		for _, a := range page.Accounts {
			if a.Id == nil {
				continue
			}
			if string(a.Status) != "ACTIVE" {
				continue
			}
			ids = append(ids, *a.Id)
		}
	}
	return ids, nil
}

func awsScanAccount(ctx context.Context, cfg aws.Config, account string, opts Options) ([]ManagedDatabase, []string) {
	regions, warns := awsSDKRegions(ctx, cfg, opts)
	var (
		out      []ManagedDatabase
		warnings = warns
	)
	for _, region := range regions {
		rcfg := cfg.Copy()
		rcfg.Region = region
		dbs, w := awsScanRegion(ctx, rcfg, region, account, opts)
		out = append(out, dbs...)
		warnings = append(warnings, w...)
	}
	return out, warnings
}

func awsSDKRegions(ctx context.Context, cfg aws.Config, opts Options) ([]string, []string) {
	if len(opts.Regions) > 0 {
		return opts.Regions, nil
	}
	if opts.AllRegions {
		client := ec2.NewFromConfig(cfg)
		out, err := client.DescribeRegions(ctx, &ec2.DescribeRegionsInput{})
		if err != nil {
			return nil, []string{fmt.Sprintf("aws ec2 describe-regions: %v", err)}
		}
		var regions []string
		for _, r := range out.Regions {
			if r.RegionName == nil {
				continue
			}
			if r.OptInStatus != nil && *r.OptInStatus == "not-opted-in" {
				continue
			}
			regions = append(regions, *r.RegionName)
		}
		return regions, nil
	}
	if cfg.Region != "" {
		return []string{cfg.Region}, nil
	}
	return []string{"us-east-1"}, nil
}

func awsScanRegion(ctx context.Context, cfg aws.Config, region, account string, opts Options) ([]ManagedDatabase, []string) {
	var (
		out      []ManagedDatabase
		warnings []string
	)
	add := func(dbs []ManagedDatabase, w []string) {
		out = append(out, dbs...)
		warnings = append(warnings, w...)
	}
	add(awsSDKRDS(ctx, cfg, region, account, opts))
	add(awsSDKElastiCache(ctx, cfg, region, account))
	add(awsSDKMemoryDB(ctx, cfg, region, account))
	add(awsSDKOpenSearch(ctx, cfg, region, account, opts))
	return out, warnings
}

func awsSDKRDS(ctx context.Context, cfg aws.Config, region, account string, opts Options) ([]ManagedDatabase, []string) {
	client := rds.NewFromConfig(cfg)
	var (
		out      []ManagedDatabase
		warnings []string
	)
	p := rds.NewDescribeDBInstancesPaginator(client, &rds.DescribeDBInstancesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return out, append(warnings, fmt.Sprintf("rds (%s): %v", region, err))
		}
		for _, i := range page.DBInstances {
			engine := aws.ToString(i.Engine)
			db := ManagedDatabase{
				Provider: ProviderAWS,
				Service:  awsServiceForEngine(engine),
				Engine:   engine,
				Name:     aws.ToString(i.DBInstanceIdentifier),
				Account:  account,
				Region:   regionOr(region, aws.ToString(i.AvailabilityZone)),
				NodeType: aws.ToString(i.DBInstanceClass),
			}
			class := aws.ToString(i.DBInstanceClass)
			if class == "db.serverless" {
				db.NodeCount = 1
				db.Notes = "serverless (ACU-based); memory not counted"
				out = append(out, db)
				continue
			}
			spec, ok := awsInstanceSpec(class)
			if !ok {
				db.NodeCount = 1
				db.Notes = "unknown instance class"
				warnings = append(warnings, fmt.Sprintf("rds %s: unknown instance class %q", db.Name, class))
				out = append(out, db)
				continue
			}
			db.VCPUPerNode = spec.VCPU
			db.MemoryGiBPerNode = spec.MemoryGiB
			db.NodeCount = 1
			if aws.ToBool(i.MultiAZ) && opts.CountStandby && aws.ToString(i.DBClusterIdentifier) == "" {
				db.NodeCount = 2
			}
			db.MonthlyCostUSD, db.CostEstimated = awsEstimateCost(db)
			out = append(out, db)
		}
	}
	return out, warnings
}

func awsSDKElastiCache(ctx context.Context, cfg aws.Config, region, account string) ([]ManagedDatabase, []string) {
	client := elasticache.NewFromConfig(cfg)
	var (
		out      []ManagedDatabase
		warnings []string
	)
	rgp := elasticache.NewDescribeReplicationGroupsPaginator(client, &elasticache.DescribeReplicationGroupsInput{})
	for rgp.HasMorePages() {
		page, err := rgp.NextPage(ctx)
		if err != nil {
			return out, append(warnings, fmt.Sprintf("elasticache replication-groups (%s): %v", region, err))
		}
		for _, g := range page.ReplicationGroups {
			nodeType := aws.ToString(g.CacheNodeType)
			db := ManagedDatabase{
				Provider: ProviderAWS, Service: "ElastiCache", Engine: "redis",
				Name: aws.ToString(g.ReplicationGroupId), Account: account, Region: region, NodeType: nodeType,
			}
			nodes := atLeastOne(len(g.MemberClusters))
			if spec, ok := awsInstanceSpec(nodeType); ok {
				db.VCPUPerNode, db.MemoryGiBPerNode, db.NodeCount = spec.VCPU, spec.MemoryGiB, nodes
				db.MonthlyCostUSD, db.CostEstimated = awsEstimateCost(db)
			} else {
				db.NodeCount, db.Notes = nodes, "unknown node type"
				warnings = append(warnings, fmt.Sprintf("elasticache %s: unknown node type %q", db.Name, nodeType))
			}
			out = append(out, db)
		}
	}

	ccp := elasticache.NewDescribeCacheClustersPaginator(client, &elasticache.DescribeCacheClustersInput{})
	for ccp.HasMorePages() {
		page, err := ccp.NextPage(ctx)
		if err != nil {
			return out, append(warnings, fmt.Sprintf("elasticache cache-clusters (%s): %v", region, err))
		}
		for _, c := range page.CacheClusters {
			if aws.ToString(c.ReplicationGroupId) != "" {
				continue // counted via replication group
			}
			nodeType := aws.ToString(c.CacheNodeType)
			db := ManagedDatabase{
				Provider: ProviderAWS, Service: "ElastiCache", Engine: aws.ToString(c.Engine),
				Name: aws.ToString(c.CacheClusterId), Account: account, Region: region, NodeType: nodeType,
			}
			nodes := atLeastOne(int(aws.ToInt32(c.NumCacheNodes)))
			if spec, ok := awsInstanceSpec(nodeType); ok {
				db.VCPUPerNode, db.MemoryGiBPerNode, db.NodeCount = spec.VCPU, spec.MemoryGiB, nodes
				db.MonthlyCostUSD, db.CostEstimated = awsEstimateCost(db)
			} else {
				db.NodeCount, db.Notes = nodes, "unknown node type"
				warnings = append(warnings, fmt.Sprintf("elasticache %s: unknown node type %q", db.Name, nodeType))
			}
			out = append(out, db)
		}
	}
	return out, warnings
}

func awsSDKMemoryDB(ctx context.Context, cfg aws.Config, region, account string) ([]ManagedDatabase, []string) {
	client := memorydb.NewFromConfig(cfg)
	var (
		out      []ManagedDatabase
		warnings []string
	)
	p := memorydb.NewDescribeClustersPaginator(client, &memorydb.DescribeClustersInput{ShowShardDetails: aws.Bool(true)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return out, append(warnings, fmt.Sprintf("memorydb (%s): %v", region, err))
		}
		for _, c := range page.Clusters {
			nodeType := aws.ToString(c.NodeType)
			nodes := 0
			for _, s := range c.Shards {
				nodes += int(aws.ToInt32(s.NumberOfNodes))
			}
			nodes = atLeastOne(nodes)
			db := ManagedDatabase{
				Provider: ProviderAWS, Service: "MemoryDB", Engine: "redis",
				Name: aws.ToString(c.Name), Account: account, Region: region, NodeType: nodeType,
			}
			if spec, ok := awsInstanceSpec(nodeType); ok {
				db.VCPUPerNode, db.MemoryGiBPerNode, db.NodeCount = spec.VCPU, spec.MemoryGiB, nodes
				db.MonthlyCostUSD, db.CostEstimated = awsEstimateCost(db)
			} else {
				db.NodeCount, db.Notes = nodes, "unknown node type"
				warnings = append(warnings, fmt.Sprintf("memorydb %s: unknown node type %q", db.Name, nodeType))
			}
			out = append(out, db)
		}
	}
	return out, warnings
}

func awsSDKOpenSearch(ctx context.Context, cfg aws.Config, region, account string, opts Options) ([]ManagedDatabase, []string) {
	client := opensearch.NewFromConfig(cfg)
	var warnings []string
	names, err := client.ListDomainNames(ctx, &opensearch.ListDomainNamesInput{})
	if err != nil {
		return nil, []string{fmt.Sprintf("opensearch list-domain-names (%s): %v", region, err)}
	}
	var domainNames []string
	for _, n := range names.DomainNames {
		if n.DomainName != nil {
			domainNames = append(domainNames, *n.DomainName)
		}
	}
	if len(domainNames) == 0 {
		return nil, nil
	}
	desc, err := client.DescribeDomains(ctx, &opensearch.DescribeDomainsInput{DomainNames: domainNames})
	if err != nil {
		return nil, []string{fmt.Sprintf("opensearch describe-domains (%s): %v", region, err)}
	}
	var out []ManagedDatabase
	for _, dom := range desc.DomainStatusList {
		if dom.ClusterConfig == nil {
			continue
		}
		cc := dom.ClusterConfig
		nodeType := string(cc.InstanceType)
		db := ManagedDatabase{
			Provider: ProviderAWS, Service: "OpenSearch", Engine: "opensearch",
			Name: aws.ToString(dom.DomainName), Account: account, Region: region, NodeType: nodeType,
		}
		dataNodes := atLeastOne(int(aws.ToInt32(cc.InstanceCount)))
		if spec, ok := awsInstanceSpec(nodeType); ok {
			db.VCPUPerNode, db.MemoryGiBPerNode, db.NodeCount = spec.VCPU, spec.MemoryGiB, dataNodes
			db.MonthlyCostUSD, db.CostEstimated = awsEstimateCost(db)
		} else {
			db.NodeCount, db.Notes = dataNodes, "unknown instance type"
			warnings = append(warnings, fmt.Sprintf("opensearch %s: unknown instance type %q", db.Name, nodeType))
		}
		out = append(out, db)
		if opts.IncludeNonData && aws.ToBool(cc.DedicatedMasterEnabled) && cc.DedicatedMasterCount != nil {
			if ms, ok := awsInstanceSpec(string(cc.DedicatedMasterType)); ok {
				out = append(out, ManagedDatabase{
					Provider: ProviderAWS, Service: "OpenSearch", Engine: "opensearch",
					Name: db.Name + " (masters)", Account: account, Region: region,
					NodeType: string(cc.DedicatedMasterType), VCPUPerNode: ms.VCPU,
					MemoryGiBPerNode: ms.MemoryGiB, NodeCount: int(aws.ToInt32(cc.DedicatedMasterCount)),
				})
			}
		}
	}
	return out, warnings
}
