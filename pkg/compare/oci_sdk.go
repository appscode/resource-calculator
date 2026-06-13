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
	"fmt"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/database"
	"github.com/oracle/oci-go-sdk/v65/mysql"
	"github.com/oracle/oci-go-sdk/v65/redis"
)

// discoverViaSDK inventories Oracle Cloud databases through oci-go-sdk using the
// default config provider (~/.oci/config or instance principals). --account is
// the compartment (or tenancy) OCID to scan.
func (d ociDiscoverer) discoverViaSDK(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	if opts.Account == "" {
		return nil, nil, fmt.Errorf("oci: --account=<compartment-or-tenancy-OCID> is required for --source=sdk (or use --source=file)")
	}
	provider := common.DefaultConfigProvider()
	compartment := opts.Account
	region := ""
	if len(opts.Regions) == 1 {
		region = opts.Regions[0]
	}
	var (
		out      []ManagedDatabase
		warnings []string
	)

	if c, err := mysql.NewDbSystemClientWithConfigurationProvider(provider); err != nil {
		warnings = append(warnings, fmt.Sprintf("oci mysql: %v", err))
	} else {
		if region != "" {
			c.SetRegion(region)
		}
		resp, err := c.ListDbSystems(ctx, mysql.ListDbSystemsRequest{CompartmentId: &compartment})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("oci mysql: %v", err))
		} else {
			for _, s := range resp.Items {
				shape := ociStr(s.ShapeName)
				db := ManagedDatabase{
					Provider: ProviderOCI, Service: "MySQL HeatWave", Engine: "mysql",
					Name: ociStr(s.DisplayName), Account: compartment, Region: region, NodeType: shape,
				}
				if spec, ok := ociMySQLSpec(shape); ok {
					db.VCPUPerNode, db.MemoryGiBPerNode, db.NodeCount = spec.VCPU, spec.MemoryGiB, 1
					if ociBool(s.IsHighlyAvailable) {
						db.NodeCount = 3
					}
					db.MonthlyCostUSD, db.CostEstimated = ociEstimateCost(db)
				} else {
					db.NodeCount, db.Notes = 1, "unknown shape"
					warnings = append(warnings, fmt.Sprintf("oci mysql %s: unknown shape %q", db.Name, shape))
				}
				out = append(out, db)
			}
		}
	}

	if c, err := database.NewDatabaseClientWithConfigurationProvider(provider); err != nil {
		warnings = append(warnings, fmt.Sprintf("oci database: %v", err))
	} else {
		if region != "" {
			c.SetRegion(region)
		}
		if resp, err := c.ListDbSystems(ctx, database.ListDbSystemsRequest{CompartmentId: &compartment}); err != nil {
			warnings = append(warnings, fmt.Sprintf("oci base-db: %v", err))
		} else {
			for _, s := range resp.Items {
				shape := ociStr(s.Shape)
				db := ManagedDatabase{
					Provider: ProviderOCI, Service: "Base Database", Engine: "oracle",
					Name: ociStr(s.DisplayName), Account: compartment, Region: region, NodeType: shape,
				}
				nodes := atLeastOne(ociInt(s.NodeCount))
				if mem := ociInt(s.MemorySizeInGBs); mem > 0 {
					db.VCPUPerNode = float64(ociInt(s.CpuCoreCount)) * 2
					db.MemoryGiBPerNode, db.NodeCount = float64(mem), nodes
					db.MonthlyCostUSD, db.CostEstimated = ociEstimateCost(db)
				} else if spec, ok := ociBaseDBSpec(shape); ok {
					db.VCPUPerNode, db.MemoryGiBPerNode, db.NodeCount = spec.VCPU, spec.MemoryGiB, nodes
					db.MonthlyCostUSD, db.CostEstimated = ociEstimateCost(db)
				} else {
					db.NodeCount, db.Notes = nodes, "unknown shape"
					warnings = append(warnings, fmt.Sprintf("oci base-db %s: unknown shape %q", db.Name, shape))
				}
				out = append(out, db)
			}
		}

		if resp, err := c.ListAutonomousDatabases(ctx, database.ListAutonomousDatabasesRequest{CompartmentId: &compartment}); err != nil {
			warnings = append(warnings, fmt.Sprintf("oci autonomous: %v", err))
		} else {
			for _, s := range resp.Items {
				name := ociStr(s.DisplayName)
				if name == "" {
					name = ociStr(s.DbName)
				}
				compute := float64(ociF32(s.ComputeCount))
				if compute <= 0 {
					compute = float64(ociInt(s.CpuCoreCount))
				}
				db := ManagedDatabase{
					Provider: ProviderOCI, Service: "Autonomous Database", Engine: "oracle",
					Name: name, Account: compartment, Region: region,
					NodeType: fmt.Sprintf("%g %s", compute, defaultStr(string(s.ComputeModel), "ECPU")),
				}
				if compute <= 0 {
					db.NodeCount, db.Notes = 1, "missing compute count"
					warnings = append(warnings, fmt.Sprintf("oci autonomous %s: missing compute count", name))
				} else {
					db.VCPUPerNode, db.MemoryGiBPerNode, db.NodeCount = compute, ociAutonomousMemoryGiB(compute), 1
					db.Notes = "memory estimated at ~8 GiB per ECPU/OCPU"
					db.MonthlyCostUSD, db.CostEstimated = ociEstimateCost(db)
				}
				out = append(out, db)
			}
		}
	}

	if c, err := redis.NewRedisClusterClientWithConfigurationProvider(provider); err != nil {
		warnings = append(warnings, fmt.Sprintf("oci cache: %v", err))
	} else {
		if region != "" {
			c.SetRegion(region)
		}
		resp, err := c.ListRedisClusters(ctx, redis.ListRedisClustersRequest{CompartmentId: &compartment})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("oci cache: %v", err))
		} else {
			for _, s := range resp.Items {
				db := ManagedDatabase{
					Provider: ProviderOCI, Service: "OCI Cache", Engine: "redis",
					Name: ociStr(s.DisplayName), Account: compartment, Region: region,
					MemoryGiBPerNode: float64(ociF32(s.NodeMemoryInGBs)), NodeCount: atLeastOne(ociInt(s.NodeCount)),
				}
				db.MonthlyCostUSD, db.CostEstimated = ociEstimateCost(db)
				out = append(out, db)
			}
		}
	}

	warnings = append(warnings, "oci: scan covers compartment "+compartment+" only; use the compartment tree for tenancy-wide scans")
	return out, warnings, nil
}

func ociStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func ociInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func ociF32(p *float32) float32 {
	if p == nil {
		return 0
	}
	return *p
}

func ociBool(p *bool) bool {
	return p != nil && *p
}
