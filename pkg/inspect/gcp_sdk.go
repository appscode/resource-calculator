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

	alloydb "google.golang.org/api/alloydb/v1"
	redis "google.golang.org/api/redis/v1"
	sqladmin "google.golang.org/api/sqladmin/v1"
)

// discoverViaSDK inventories Google Cloud databases via google.golang.org/api
// (Cloud SQL, Memorystore for Redis, AlloyDB) using Application Default
// Credentials. --account selects the project (required).
func (d gcpDiscoverer) discoverViaSDK(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	project := opts.Account
	if project == "" {
		return nil, nil, fmt.Errorf("gcp: --account=<project> is required for --source=sdk (or use --source=file)")
	}
	var (
		out      []ManagedDatabase
		warnings []string
	)

	if svc, err := sqladmin.NewService(ctx); err != nil {
		warnings = append(warnings, fmt.Sprintf("gcp cloudsql: %v", err))
	} else {
		err := svc.Instances.List(project).Pages(ctx, func(page *sqladmin.InstancesListResponse) error {
			for _, i := range page.Items {
				db := ManagedDatabase{
					Provider: ProviderGCP, Service: "Cloud SQL", Engine: gcpEngine(i.DatabaseVersion),
					Name: i.Name, Account: project, Region: i.Region,
				}
				if i.Settings != nil {
					db.NodeType = i.Settings.Tier
				}
				spec, ok := gcpCloudSQLSpec(db.NodeType)
				if !ok {
					db.NodeCount, db.Notes = 1, "unknown tier"
					warnings = append(warnings, fmt.Sprintf("cloudsql %s: unknown tier %q", i.Name, db.NodeType))
					out = append(out, db)
					continue
				}
				db.VCPUPerNode, db.MemoryGiBPerNode, db.NodeCount = spec.VCPU, spec.MemoryGiB, 1
				if opts.CountStandby && i.Settings != nil &&
					strings.EqualFold(i.Settings.AvailabilityType, "REGIONAL") &&
					!strings.EqualFold(i.InstanceType, "READ_REPLICA") {
					db.NodeCount = 2
				}
				db.MonthlyCostUSD, db.CostEstimated = gcpEstimateCost(db)
				out = append(out, db)
			}
			return nil
		})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("gcp cloudsql list: %v", err))
		}
	}

	if svc, err := redis.NewService(ctx); err != nil {
		warnings = append(warnings, fmt.Sprintf("gcp memorystore: %v", err))
	} else {
		parent := "projects/" + project + "/locations/-"
		err := svc.Projects.Locations.Instances.List(parent).Pages(ctx, func(page *redis.ListInstancesResponse) error {
			for _, i := range page.Instances {
				name := i.Name
				if idx := strings.LastIndex(name, "/"); idx >= 0 {
					name = name[idx+1:]
				}
				nodes := 1
				if opts.CountStandby {
					nodes = 1 + int(i.ReplicaCount)
				}
				db := ManagedDatabase{
					Provider: ProviderGCP, Service: "Memorystore", Engine: "redis",
					Name: name, Account: project, Region: i.LocationId, NodeType: i.Tier,
					MemoryGiBPerNode: float64(i.MemorySizeGb), NodeCount: nodes,
				}
				db.MonthlyCostUSD, db.CostEstimated = gcpEstimateCost(db)
				out = append(out, db)
			}
			return nil
		})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("gcp memorystore list: %v", err))
		}
	}

	if svc, err := alloydb.NewService(ctx); err != nil {
		warnings = append(warnings, fmt.Sprintf("gcp alloydb: %v", err))
	} else {
		parent := "projects/" + project + "/locations/-/clusters/-"
		err := svc.Projects.Locations.Clusters.Instances.List(parent).Pages(ctx, func(page *alloydb.ListInstancesResponse) error {
			for _, i := range page.Instances {
				if i.MachineConfig == nil || i.MachineConfig.CpuCount <= 0 {
					continue
				}
				name := i.Name
				region := ""
				if parts := strings.Split(name, "/"); len(parts) >= 6 {
					region = parts[3]
					name = parts[len(parts)-1]
				}
				cpu := float64(i.MachineConfig.CpuCount)
				nodes := 1
				if strings.EqualFold(i.InstanceType, "READ_POOL") && i.ReadPoolConfig != nil {
					nodes = atLeastOne(int(i.ReadPoolConfig.NodeCount))
				}
				db := ManagedDatabase{
					Provider: ProviderGCP, Service: "AlloyDB", Engine: "postgres",
					Name: name, Account: project, Region: region,
					NodeType:    fmt.Sprintf("%g vCPU", cpu),
					VCPUPerNode: cpu, MemoryGiBPerNode: gcpAlloyDBMemoryGiB(cpu), NodeCount: nodes,
				}
				db.MonthlyCostUSD, db.CostEstimated = gcpEstimateCost(db)
				out = append(out, db)
			}
			return nil
		})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("gcp alloydb list: %v", err))
		}
	}

	return out, warnings, nil
}
