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

// gcpDiscoverer inventories Google Cloud managed databases via the `gcloud` CLI
// (or a JSON bundle of `gcloud ... list --format=json`): Cloud SQL, AlloyDB and
// Memorystore for Redis. Spanner/Bigtable are abstracted compute and out of the
// memory-based comparison.
type gcpDiscoverer struct{}

func (gcpDiscoverer) Provider() Provider { return ProviderGCP }

func (d gcpDiscoverer) Discover(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	switch opts.Source {
	case SourceSDK:
		return d.discoverViaSDK(ctx, opts)
	case SourceFile:
		return discoverViaFile(opts.FromFile, gcpCollectors(), opts)
	case SourceCLI:
		if !cliAvailable("gcloud") {
			return nil, nil, fmt.Errorf("gcloud CLI not found on PATH; install it or use --source=file with exported JSON")
		}
		return d.discoverViaCLI(ctx, opts)
	case SourceAuto:
		if opts.FromFile != "" {
			return discoverViaFile(opts.FromFile, gcpCollectors(), opts)
		}
		return d.discoverViaSDK(ctx, opts) // SDK is the preferred live source
	default:
		return nil, nil, fmt.Errorf("gcp: unsupported source %q", opts.Source)
	}
}

func (d gcpDiscoverer) discoverViaCLI(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	var (
		out      []ManagedDatabase
		warnings []string
	)
	for _, c := range gcpCollectors() {
		args := append([]string{}, c.args...)
		if opts.Account != "" {
			args = append(args, "--project", opts.Account)
		}
		raw, err := runCLIJSON(ctx, opts, "gcloud", args...)
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

func gcpCollectors() []collector {
	return []collector{
		{key: "cloudsql-instances", args: []string{"sql", "instances", "list", "--format=json"}, parse: parseGCPCloudSQL},
		{key: "memorystore-redis", args: []string{"redis", "instances", "list", "--region=-", "--format=json"}, parse: parseGCPMemorystore},
		{key: "alloydb-instances", args: []string{"alloydb", "instances", "list", "--region=-", "--cluster=-", "--format=json"}, parse: parseGCPAlloyDB},
	}
}

type gcpCloudSQLInstance struct {
	Name            string `json:"name"`
	Region          string `json:"region"`
	DatabaseVersion string `json:"databaseVersion"`
	InstanceType    string `json:"instanceType"`
	Settings        struct {
		Tier             string `json:"tier"`
		AvailabilityType string `json:"availabilityType"`
	} `json:"settings"`
}

func parseGCPCloudSQL(raw []byte, _ string, opts Options) ([]ManagedDatabase, []string, error) {
	var items []gcpCloudSQLInstance
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, nil, err
	}
	var out []ManagedDatabase
	var warnings []string
	for _, i := range items {
		db := ManagedDatabase{
			Provider: ProviderGCP,
			Service:  "Cloud SQL",
			Engine:   gcpEngine(i.DatabaseVersion),
			Name:     i.Name,
			Account:  opts.Account,
			Region:   i.Region,
			NodeType: i.Settings.Tier,
		}
		spec, ok := gcpCloudSQLSpec(i.Settings.Tier)
		if !ok {
			db.NodeCount = 1
			db.Notes = "unknown tier"
			warnings = append(warnings, fmt.Sprintf("cloudsql %s: unknown tier %q", i.Name, i.Settings.Tier))
			out = append(out, db)
			continue
		}
		db.VCPUPerNode = spec.VCPU
		db.MemoryGiBPerNode = spec.MemoryGiB
		db.NodeCount = 1
		if opts.CountStandby && strings.EqualFold(i.Settings.AvailabilityType, "REGIONAL") && !strings.EqualFold(i.InstanceType, "READ_REPLICA") {
			db.NodeCount = 2 // regional HA standby
		}
		db.MonthlyCostUSD, db.CostEstimated = gcpEstimateCost(db)
		out = append(out, db)
	}
	return out, warnings, nil
}

func gcpEngine(version string) string {
	v := strings.ToUpper(version)
	switch {
	case strings.HasPrefix(v, "POSTGRES"):
		return "postgres"
	case strings.HasPrefix(v, "MYSQL"):
		return "mysql"
	case strings.HasPrefix(v, "SQLSERVER"):
		return "sqlserver"
	default:
		return strings.ToLower(version)
	}
}

type gcpMemorystoreInstance struct {
	Name         string `json:"name"`
	LocationID   string `json:"locationId"`
	MemorySizeGb int    `json:"memorySizeGb"`
	Tier         string `json:"tier"`
	ReplicaCount int    `json:"replicaCount"`
}

func parseGCPMemorystore(raw []byte, _ string, opts Options) ([]ManagedDatabase, []string, error) {
	var items []gcpMemorystoreInstance
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, nil, err
	}
	var out []ManagedDatabase
	var warnings []string
	for _, i := range items {
		// name is "projects/p/locations/l/instances/n"; keep the short name.
		name := i.Name
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		nodes := 1
		if opts.CountStandby {
			nodes = 1 + i.ReplicaCount
		}
		db := ManagedDatabase{
			Provider:         ProviderGCP,
			Service:          "Memorystore",
			Engine:           "redis",
			Name:             name,
			Account:          opts.Account,
			Region:           i.LocationID,
			NodeType:         i.Tier,
			MemoryGiBPerNode: float64(i.MemorySizeGb),
			NodeCount:        nodes,
		}
		db.MonthlyCostUSD, db.CostEstimated = gcpEstimateCost(db)
		out = append(out, db)
	}
	return out, warnings, nil
}

type gcpAlloyDBInstance struct {
	Name          string `json:"name"`
	InstanceType  string `json:"instanceType"`
	MachineConfig struct {
		CPUCount float64 `json:"cpuCount"`
	} `json:"machineConfig"`
	ReadPoolConfig struct {
		NodeCount int `json:"nodeCount"`
	} `json:"readPoolConfig"`
}

func parseGCPAlloyDB(raw []byte, _ string, opts Options) ([]ManagedDatabase, []string, error) {
	var items []gcpAlloyDBInstance
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, nil, err
	}
	var out []ManagedDatabase
	var warnings []string
	for _, i := range items {
		name := i.Name
		region := ""
		// name is "projects/p/locations/l/clusters/c/instances/n".
		if parts := strings.Split(name, "/"); len(parts) >= 6 {
			region = parts[3]
			name = parts[len(parts)-1]
		}
		if i.MachineConfig.CPUCount <= 0 {
			warnings = append(warnings, fmt.Sprintf("alloydb %s: missing cpuCount", name))
			continue
		}
		nodes := 1
		if strings.EqualFold(i.InstanceType, "READ_POOL") {
			nodes = atLeastOne(i.ReadPoolConfig.NodeCount)
		}
		db := ManagedDatabase{
			Provider:         ProviderGCP,
			Service:          "AlloyDB",
			Engine:           "postgres",
			Name:             name,
			Account:          opts.Account,
			Region:           region,
			NodeType:         fmt.Sprintf("%g vCPU", i.MachineConfig.CPUCount),
			VCPUPerNode:      i.MachineConfig.CPUCount,
			MemoryGiBPerNode: gcpAlloyDBMemoryGiB(i.MachineConfig.CPUCount),
			NodeCount:        nodes,
		}
		db.MonthlyCostUSD, db.CostEstimated = gcpEstimateCost(db)
		out = append(out, db)
	}
	return out, warnings, nil
}

var gcpServiceRatePerGiBHour = map[string]float64{
	"Cloud SQL":   0.0171,
	"AlloyDB":     0.0223,
	"Memorystore": 0.068,
}

func gcpEstimateCost(db ManagedDatabase) (float64, bool) {
	rate, ok := gcpServiceRatePerGiBHour[db.Service]
	if !ok || db.MemoryGiBPerNode <= 0 {
		return 0, false
	}
	return db.TotalMemoryGiB() * rate * hoursPerMonth, true
}
