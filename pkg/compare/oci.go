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
)

// ociDiscoverer inventories Oracle Cloud managed databases via the `oci` CLI
// (or a JSON bundle of `oci ... list` output): MySQL HeatWave, Base Database,
// Autonomous Database and OCI Cache. OCI list calls are compartment scoped, so
// --account must be a compartment (or tenancy) OCID.
type ociDiscoverer struct{}

func (ociDiscoverer) Provider() Provider { return ProviderOCI }

func (d ociDiscoverer) Discover(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	switch opts.Source {
	case SourceSDK:
		return d.discoverViaSDK(ctx, opts)
	case SourceFile:
		return discoverViaFile(opts.FromFile, ociCollectors(), opts)
	case SourceCLI:
		if !cliAvailable("oci") {
			return nil, nil, fmt.Errorf("oci CLI not found on PATH; install it or use --source=file with exported JSON")
		}
		if opts.Account == "" {
			return nil, nil, fmt.Errorf("oci: --account=<compartment-or-tenancy-OCID> is required for live discovery")
		}
		return d.discoverViaCLI(ctx, opts)
	case SourceAuto:
		if opts.FromFile != "" {
			return discoverViaFile(opts.FromFile, ociCollectors(), opts)
		}
		return d.discoverViaSDK(ctx, opts) // SDK is the preferred live source
	default:
		return nil, nil, fmt.Errorf("oci: unsupported source %q", opts.Source)
	}
}

func (d ociDiscoverer) discoverViaCLI(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	var (
		out      []ManagedDatabase
		warnings []string
	)
	for _, c := range ociCollectors() {
		args := append([]string{}, c.args...)
		args = append(args, "--compartment-id", opts.Account)
		if len(opts.Regions) == 1 {
			args = append(args, "--region", opts.Regions[0])
		}
		raw, err := runCLIJSON(ctx, opts, "oci", args...)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", c.key, err))
			continue
		}
		dbs, warns, err := c.parse(raw, opts.regionHint(), opts)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", c.key, err))
			continue
		}
		out = append(out, dbs...)
		warnings = append(warnings, warns...)
	}
	warnings = append(warnings, "oci: scan covers compartment "+opts.Account+" only; use the compartment tree / search service for tenancy-wide scans (see docs/inspect.md)")
	return out, warnings, nil
}

func ociCollectors() []collector {
	return []collector{
		{key: "mysql-db-systems", args: []string{"mysql", "db-system", "list", "--output", "json"}, parse: parseOCIMySQL},
		{key: "base-db-systems", args: []string{"db", "system", "list", "--output", "json"}, parse: parseOCIBaseDB},
		{key: "autonomous-databases", args: []string{"db", "autonomous-database", "list", "--output", "json"}, parse: parseOCIAutonomous},
		{key: "cache-clusters", args: []string{"redis", "redis-cluster", "list", "--output", "json"}, parse: parseOCICache},
	}
}

type ociMySQLList struct {
	Data []struct {
		DisplayName       string `json:"display-name"`
		ShapeName         string `json:"shape-name"`
		IsHighlyAvailable bool   `json:"is-highly-available"`
		Region            string `json:"region"`
	} `json:"data"`
}

func parseOCIMySQL(raw []byte, region string, opts Options) ([]ManagedDatabase, []string, error) {
	var in ociMySQLList
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, nil, err
	}
	var out []ManagedDatabase
	var warnings []string
	for _, s := range in.Data {
		db := ManagedDatabase{
			Provider: ProviderOCI,
			Service:  "MySQL HeatWave",
			Engine:   "mysql",
			Name:     s.DisplayName,
			Account:  opts.Account,
			Region:   regionOr(s.Region, region),
			NodeType: s.ShapeName,
		}
		spec, ok := ociMySQLSpec(s.ShapeName)
		if !ok {
			db.NodeCount = 1
			db.Notes = "unknown shape"
			warnings = append(warnings, fmt.Sprintf("oci mysql %s: unknown shape %q", s.DisplayName, s.ShapeName))
			out = append(out, db)
			continue
		}
		db.VCPUPerNode = spec.VCPU
		db.MemoryGiBPerNode = spec.MemoryGiB
		db.NodeCount = 1
		if s.IsHighlyAvailable {
			db.NodeCount = 3 // HA = 1 primary + 2 secondaries
		}
		db.MonthlyCostUSD, db.CostEstimated = ociEstimateCost(db)
		out = append(out, db)
	}
	return out, warnings, nil
}

type ociBaseDBList struct {
	Data []struct {
		DisplayName    string  `json:"display-name"`
		Shape          string  `json:"shape"`
		NodeCount      int     `json:"node-count"`
		CPUCoreCount   int     `json:"cpu-core-count"`
		MemorySizeInGB float64 `json:"memory-size-in-gbs"`
		Region         string  `json:"region"`
	} `json:"data"`
}

func parseOCIBaseDB(raw []byte, region string, opts Options) ([]ManagedDatabase, []string, error) {
	var in ociBaseDBList
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, nil, err
	}
	var out []ManagedDatabase
	var warnings []string
	for _, s := range in.Data {
		db := ManagedDatabase{
			Provider: ProviderOCI,
			Service:  "Base Database",
			Engine:   "oracle",
			Name:     s.DisplayName,
			Account:  opts.Account,
			Region:   regionOr(s.Region, region),
			NodeType: s.Shape,
		}
		nodes := atLeastOne(s.NodeCount)
		if s.MemorySizeInGB > 0 {
			// Flex shapes report memory directly (already the per-node value).
			db.VCPUPerNode = float64(s.CPUCoreCount) * 2 // OCPU -> vCPU
			db.MemoryGiBPerNode = s.MemorySizeInGB
			db.NodeCount = nodes
		} else if spec, ok := ociBaseDBSpec(s.Shape); ok {
			db.VCPUPerNode = spec.VCPU
			db.MemoryGiBPerNode = spec.MemoryGiB
			db.NodeCount = nodes
		} else {
			db.NodeCount = nodes
			db.Notes = "unknown shape"
			warnings = append(warnings, fmt.Sprintf("oci base-db %s: unknown shape %q", s.DisplayName, s.Shape))
			out = append(out, db)
			continue
		}
		db.MonthlyCostUSD, db.CostEstimated = ociEstimateCost(db)
		out = append(out, db)
	}
	return out, warnings, nil
}

type ociAutonomousList struct {
	Data []struct {
		DBName       string  `json:"db-name"`
		DisplayName  string  `json:"display-name"`
		ComputeModel string  `json:"compute-model"`
		ComputeCount float64 `json:"compute-count"`
		CPUCoreCount float64 `json:"cpu-core-count"`
		Region       string  `json:"region"`
	} `json:"data"`
}

func parseOCIAutonomous(raw []byte, region string, opts Options) ([]ManagedDatabase, []string, error) {
	var in ociAutonomousList
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, nil, err
	}
	var out []ManagedDatabase
	var warnings []string
	for _, s := range in.Data {
		name := s.DisplayName
		if name == "" {
			name = s.DBName
		}
		compute := s.ComputeCount
		if compute <= 0 {
			compute = s.CPUCoreCount
		}
		db := ManagedDatabase{
			Provider: ProviderOCI,
			Service:  "Autonomous Database",
			Engine:   "oracle",
			Name:     name,
			Account:  opts.Account,
			Region:   regionOr(s.Region, region),
			NodeType: fmt.Sprintf("%g %s", compute, defaultStr(s.ComputeModel, "ECPU")),
		}
		if compute <= 0 {
			db.NodeCount = 1
			db.Notes = "missing compute count"
			warnings = append(warnings, fmt.Sprintf("oci autonomous %s: missing compute count", name))
			out = append(out, db)
			continue
		}
		db.VCPUPerNode = compute
		db.MemoryGiBPerNode = ociAutonomousMemoryGiB(compute)
		db.NodeCount = 1
		db.Notes = "memory estimated at ~8 GiB per ECPU/OCPU"
		db.MonthlyCostUSD, db.CostEstimated = ociEstimateCost(db)
		out = append(out, db)
	}
	return out, warnings, nil
}

type ociCacheList struct {
	Data []struct {
		DisplayName     string  `json:"display-name"`
		NodeCount       int     `json:"node-count"`
		NodeMemoryInGBs float64 `json:"node-memory-in-gbs"`
		Region          string  `json:"region"`
	} `json:"data"`
}

func parseOCICache(raw []byte, region string, opts Options) ([]ManagedDatabase, []string, error) {
	var in ociCacheList
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, nil, err
	}
	var out []ManagedDatabase
	var warnings []string
	for _, s := range in.Data {
		db := ManagedDatabase{
			Provider:         ProviderOCI,
			Service:          "OCI Cache",
			Engine:           "redis",
			Name:             s.DisplayName,
			Account:          opts.Account,
			Region:           regionOr(s.Region, region),
			MemoryGiBPerNode: s.NodeMemoryInGBs,
			NodeCount:        atLeastOne(s.NodeCount),
		}
		if s.NodeMemoryInGBs <= 0 {
			db.Notes = "missing node memory"
			warnings = append(warnings, fmt.Sprintf("oci cache %s: missing node memory", s.DisplayName))
		}
		db.MonthlyCostUSD, db.CostEstimated = ociEstimateCost(db)
		out = append(out, db)
	}
	return out, warnings, nil
}

var ociServiceRatePerGiBHour = map[string]float64{
	"MySQL HeatWave":      0.0052,
	"Base Database":       0.0100,
	"Autonomous Database": 0.0200,
	"OCI Cache":           0.0100,
}

func ociEstimateCost(db ManagedDatabase) (float64, bool) {
	rate, ok := ociServiceRatePerGiBHour[db.Service]
	if !ok || db.MemoryGiBPerNode <= 0 {
		return 0, false
	}
	return db.TotalMemoryGiB() * rate * hoursPerMonth, true
}

func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
