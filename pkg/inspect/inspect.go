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

// Package inspect inventories managed database services running on public
// clouds and DBaaS vendors and reports each one's allocated CPU and memory
// plus the estate totals. Every managed database is reduced to (CPU and memory
// per node x node count) -- for a clustered database the allocation is
// "replicas x size per replica" (a 3 replica PostgreSQL with 8 GiB per replica
// counts as 24 GiB) -- and summed across the whole estate.
package inspect

import (
	"sort"
	"time"
)

// Provider identifies a managed database vendor that this package can inventory.
type Provider string

const (
	ProviderAWS        Provider = "aws"
	ProviderAzure      Provider = "azure"
	ProviderGCP        Provider = "gcp"
	ProviderOCI        Provider = "oci"
	ProviderAtlas      Provider = "atlas"
	ProviderElastic    Provider = "elastic"
	ProviderClickHouse Provider = "clickhouse"
)

// AllProviders is the canonical ordered list of supported providers.
var AllProviders = []Provider{
	ProviderAWS,
	ProviderAzure,
	ProviderGCP,
	ProviderOCI,
	ProviderAtlas,
	ProviderElastic,
	ProviderClickHouse,
}

// DisplayName returns a human friendly label for the provider.
func (p Provider) DisplayName() string {
	switch p {
	case ProviderAWS:
		return "Amazon Web Services"
	case ProviderAzure:
		return "Microsoft Azure"
	case ProviderGCP:
		return "Google Cloud"
	case ProviderOCI:
		return "Oracle Cloud"
	case ProviderAtlas:
		return "MongoDB Atlas"
	case ProviderElastic:
		return "Elastic Cloud"
	case ProviderClickHouse:
		return "ClickHouse Cloud"
	default:
		return string(p)
	}
}

// ManagedDatabase is the normalized representation of a single logical managed
// database (an RDS instance, an Aurora/DocumentDB cluster, an Atlas cluster, an
// Elastic deployment, a ClickHouse service, ...). Every provider discoverer
// reduces its native objects to this shape so the pricing engine is provider
// agnostic.
type ManagedDatabase struct {
	Provider Provider `json:"provider"`
	// Service is the managed offering, e.g. "RDS", "ElastiCache", "Cloud SQL".
	Service string `json:"service"`
	// Engine is the database engine where known, e.g. "postgres", "redis".
	Engine string `json:"engine,omitempty"`
	// Name is the user visible identifier of the database.
	Name string `json:"name"`
	// Account is the owning account/subscription/project/tenancy/org id.
	Account string `json:"account,omitempty"`
	// Region is the primary region of the database.
	Region string `json:"region,omitempty"`
	// NodeType is the instance class / SKU / tier (informational, e.g.
	// "db.r6g.large", "Standard_D4ds_v5", "M30").
	NodeType string `json:"nodeType,omitempty"`
	// VCPUPerNode is the vCPU count of a single node (0 when not derivable).
	VCPUPerNode float64 `json:"vCPUPerNode,omitempty"`
	// MemoryGiBPerNode is the RAM of a single node in GiB.
	MemoryGiBPerNode float64 `json:"memoryGiBPerNode"`
	// NodeCount is the number of billable nodes (replicas/shards x replicas/HA
	// standbys) that make up this logical database.
	NodeCount int `json:"nodeCount"`
	// MonthlyCostUSD is the estimated current managed-service compute cost.
	// Zero means unknown.
	MonthlyCostUSD float64 `json:"monthlyCostUSD,omitempty"`
	// CostEstimated is true when MonthlyCostUSD came from a bundled list-price
	// table rather than the user's actual bill.
	CostEstimated bool `json:"costEstimated,omitempty"`
	// Notes carries per-database warnings (unknown type, serverless, ...).
	Notes string `json:"notes,omitempty"`
}

// TotalMemoryGiB is the KubeDB billable memory for this database: the memory of
// one node multiplied by the number of nodes.
func (d ManagedDatabase) TotalMemoryGiB() float64 {
	return d.MemoryGiBPerNode * float64(d.NodeCount)
}

// TotalVCPU is the aggregate vCPU across all nodes of this database.
func (d ManagedDatabase) TotalVCPU() float64 {
	return d.VCPUPerNode * float64(d.NodeCount)
}

// Report is the full result of an inspection: the discovered databases plus the
// aggregated CPU and memory totals.
type Report struct {
	GeneratedAt time.Time `json:"generatedAt"`
	// Scope is the provider name, or "all" for an aggregated report.
	Scope string `json:"scope"`
	// SelfHosted marks a report for in-cluster, self-hosted databases (operators
	// and Bitnami/Chainguard/Docker Hardened Images); it adds a per-operator
	// breakdown to the text output.
	SelfHosted bool              `json:"selfHosted,omitempty"`
	Databases  []ManagedDatabase `json:"databases"`

	DatabaseCount  int     `json:"databaseCount"`
	NodeCount      int     `json:"nodeCount"`
	TotalVCPU      float64 `json:"totalVCPU"`
	TotalMemoryGiB float64 `json:"totalMemoryGiB"`

	// CurrentMonthlyUSD is the sum of estimated managed-service costs (cloud
	// providers only). It is only meaningful when CostKnown is true.
	CurrentMonthlyUSD float64 `json:"currentMonthlyUSD,omitempty"`
	CostKnown         bool    `json:"costKnown"`
	// CostPartial is true when some databases had no price and were excluded
	// from CurrentMonthlyUSD.
	CostPartial bool `json:"costPartial,omitempty"`

	Warnings []string `json:"warnings,omitempty"`
}

// BuildReport aggregates discovered databases into a Report: the per-database CPU
// and memory plus the totals. Cloud-provider databases also carry an estimated
// managed monthly cost.
func BuildReport(scope string, dbs []ManagedDatabase, warnings []string) *Report {
	r := &Report{
		GeneratedAt:   time.Now().UTC(),
		Scope:         scope,
		Databases:     dbs,
		DatabaseCount: len(dbs),
		Warnings:      append([]string(nil), warnings...),
	}

	var pricedCount int
	for _, d := range dbs {
		r.NodeCount += d.NodeCount
		r.TotalVCPU += d.TotalVCPU()
		r.TotalMemoryGiB += d.TotalMemoryGiB()
		if d.MonthlyCostUSD > 0 {
			r.CurrentMonthlyUSD += d.MonthlyCostUSD
			pricedCount++
		}
	}
	r.CostKnown = pricedCount > 0
	r.CostPartial = pricedCount > 0 && pricedCount < len(dbs)

	sortDatabases(r.Databases)
	return r
}

// sortDatabases orders databases by largest memory footprint first so the most
// impactful migration candidates surface at the top of the report.
func sortDatabases(dbs []ManagedDatabase) {
	sort.SliceStable(dbs, func(i, j int) bool {
		mi, mj := dbs[i].TotalMemoryGiB(), dbs[j].TotalMemoryGiB()
		if mi != mj {
			return mi > mj
		}
		if dbs[i].Provider != dbs[j].Provider {
			return dbs[i].Provider < dbs[j].Provider
		}
		return dbs[i].Name < dbs[j].Name
	})
}
