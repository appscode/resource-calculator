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

// Package compare inventories managed database services running on public
// clouds and DBaaS vendors, normalizes their compute footprint, and estimates
// how much could be saved by migrating them to KubeDB.
//
// KubeDB is licensed on a single metric: the total memory allocated to
// database servers. For a clustered database the billable memory is
// "replicas x memory per replica" (a 3 replica PostgreSQL with 8 GiB per
// replica counts as 24 GiB). This package mirrors that model: every managed
// database is reduced to (memory per node x node count), summed across the
// whole estate, and the KubeDB cost is derived from that single number.
package compare

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

// KubeDBPricing captures the KubeDB licensing model: a flat USD rate per GiB of
// database-server memory per month, with separate production and
// non-production rates and a production memory floor.
//
// The public rate is quote-based (see https://kubedb.com/pricing), so the rates
// here default to zero and must be supplied by the caller (typically from an
// AppsCode contract). When the rate is zero the savings columns are omitted and
// the report still shows the discovered memory footprint and current spend.
type KubeDBPricing struct {
	// ProdRateUSDPerGiBMonth is the USD/GiB/month rate for production clusters.
	ProdRateUSDPerGiBMonth float64
	// NonProdRateUSDPerGiBMonth is the USD/GiB/month rate for non-production.
	NonProdRateUSDPerGiBMonth float64
	// Prod selects the production rate and applies MinProdGiB.
	Prod bool
	// MinProdGiB is the minimum billed memory for production (KubeDB lists a
	// 100 GiB production minimum).
	MinProdGiB float64
}

// DefaultMinProdGiB is the production memory floor published by KubeDB pricing.
const DefaultMinProdGiB = 100

// Rate returns the applicable USD/GiB/month rate.
func (p KubeDBPricing) Rate() float64 {
	if p.Prod {
		return p.ProdRateUSDPerGiBMonth
	}
	return p.NonProdRateUSDPerGiBMonth
}

// HasRate reports whether a usable (non-zero) rate is configured.
func (p KubeDBPricing) HasRate() bool {
	return p.Rate() > 0
}

// BillableGiB applies the production minimum to the discovered memory.
func (p KubeDBPricing) BillableGiB(totalGiB float64) float64 {
	if p.Prod && totalGiB < p.MinProdGiB {
		return p.MinProdGiB
	}
	return totalGiB
}

// MonthlyCost returns the estimated KubeDB monthly license cost for the given
// total database-server memory.
func (p KubeDBPricing) MonthlyCost(totalGiB float64) float64 {
	return p.BillableGiB(totalGiB) * p.Rate()
}

// Report is the full result of a comparison: the discovered databases plus the
// aggregated memory, current spend, KubeDB cost and savings.
type Report struct {
	GeneratedAt time.Time `json:"generatedAt"`
	// Scope is the provider name, or "all" for an aggregated report.
	Scope     string            `json:"scope"`
	Databases []ManagedDatabase `json:"databases"`

	DatabaseCount int     `json:"databaseCount"`
	NodeCount     int     `json:"nodeCount"`
	TotalVCPU     float64 `json:"totalVCPU"`
	// TotalMemoryGiB is the raw discovered database-server memory.
	TotalMemoryGiB float64 `json:"totalMemoryGiB"`
	// BillableMemoryGiB is TotalMemoryGiB after applying the production floor.
	BillableMemoryGiB float64 `json:"billableMemoryGiB"`

	// CurrentMonthlyUSD is the sum of estimated managed-service costs. It is
	// only meaningful when CostKnown is true.
	CurrentMonthlyUSD float64 `json:"currentMonthlyUSD,omitempty"`
	CostKnown         bool    `json:"costKnown"`
	// CostPartial is true when some databases had no price and were excluded
	// from CurrentMonthlyUSD.
	CostPartial bool `json:"costPartial,omitempty"`

	// KubeDBMonthlyUSD and savings are only set when the rate is configured.
	RateConfigured    bool    `json:"rateConfigured"`
	Prod              bool    `json:"prod"`
	KubeDBRateUSD     float64 `json:"kubeDBRateUSDPerGiBMonth,omitempty"`
	KubeDBMonthlyUSD  float64 `json:"kubeDBMonthlyUSD,omitempty"`
	MonthlySavingsUSD float64 `json:"monthlySavingsUSD,omitempty"`
	AnnualSavingsUSD  float64 `json:"annualSavingsUSD,omitempty"`
	SavingsPercent    float64 `json:"savingsPercent,omitempty"`

	Warnings []string `json:"warnings,omitempty"`
}

// BuildReport aggregates a set of discovered databases into a Report using the
// supplied KubeDB pricing. The savings model is "current managed spend minus
// KubeDB license cost" (the headline figure requested for the tool).
func BuildReport(scope string, dbs []ManagedDatabase, pricing KubeDBPricing, warnings []string) *Report {
	r := &Report{
		GeneratedAt:    time.Now().UTC(),
		Scope:          scope,
		Databases:      dbs,
		DatabaseCount:  len(dbs),
		Prod:           pricing.Prod,
		RateConfigured: pricing.HasRate(),
		Warnings:       append([]string(nil), warnings...),
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

	r.BillableMemoryGiB = pricing.BillableGiB(r.TotalMemoryGiB)
	r.CostKnown = pricedCount > 0
	r.CostPartial = pricedCount > 0 && pricedCount < len(dbs)

	if pricing.HasRate() {
		r.KubeDBRateUSD = pricing.Rate()
		r.KubeDBMonthlyUSD = pricing.MonthlyCost(r.TotalMemoryGiB)
		if r.CostKnown {
			r.MonthlySavingsUSD = r.CurrentMonthlyUSD - r.KubeDBMonthlyUSD
			r.AnnualSavingsUSD = r.MonthlySavingsUSD * 12
			if r.CurrentMonthlyUSD > 0 {
				r.SavingsPercent = r.MonthlySavingsUSD / r.CurrentMonthlyUSD * 100
			}
		}
	}

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
