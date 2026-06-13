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

package cmds

import (
	"context"
	"fmt"
	"os"
	"time"

	"kubeops.dev/resource-calculator/pkg/compare"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// compareFlags holds the flags shared by every `compare` subcommand plus the
// per-vendor credentials used by the DBaaS providers.
type compareFlags struct {
	source         string
	fromFile       string
	output         string
	account        string
	regions        []string
	allRegions     bool
	org            bool
	countStandby   bool
	includeNonData bool
	timeout        time.Duration

	rateProd    float64
	rateNonProd float64
	prod        bool

	atlas      compare.AtlasCreds
	elastic    compare.ElasticCreds
	clickhouse compare.ClickHouseCreds
}

func (f *compareFlags) options() compare.Options {
	return compare.Options{
		Source:         compare.Source(f.source),
		FromFile:       f.fromFile,
		Account:        f.account,
		Regions:        f.regions,
		AllRegions:     f.allRegions,
		Org:            f.org,
		CountStandby:   f.countStandby,
		IncludeNonData: f.includeNonData,
		Timeout:        f.timeout,
		Atlas:          f.atlas,
		Elastic:        f.elastic,
		ClickHouse:     f.clickhouse,
	}
}

func (f *compareFlags) pricing() compare.KubeDBPricing {
	return compare.KubeDBPricing{
		ProdRateUSDPerGiBMonth:    f.rateProd,
		NonProdRateUSDPerGiBMonth: f.rateNonProd,
		Prod:                      f.prod,
		MinProdGiB:                compare.DefaultMinProdGiB,
	}
}

// NewCmdCompare builds the `compare` command tree: one subcommand per supported
// managed-database provider, `all`, and `operators` (in-cluster scan).
func NewCmdCompare(clientGetter genericclioptions.RESTClientGetter) *cobra.Command {
	f := &compareFlags{
		source:       string(compare.SourceAuto),
		output:       "text",
		countStandby: true,
		timeout:      60 * time.Second,
	}

	cmd := &cobra.Command{
		Use:   "compare",
		Short: "Estimate KubeDB migration savings for managed databases on public clouds and DBaaS",
		Long: `Inventory managed databases on a cloud account or DBaaS organization,
sum the memory allocated to their database servers (replicas x memory per
replica, the metric KubeDB is licensed on), and estimate how much could be
saved by migrating them to KubeDB.

Discovery is layered (--source): sdk uses the provider's official SDK, cli/rest
call its live API, and file parses previously exported JSON (--from-file). auto
picks file when --from-file is set, otherwise the SDK (REST for ClickHouse).`,
		DisableAutoGenTag: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	pf := cmd.PersistentFlags()
	pf.StringVar(&f.source, "source", f.source, "Discovery source: auto, sdk, cli, rest, file")
	pf.StringVar(&f.fromFile, "from-file", f.fromFile, "Path to exported JSON inventory (for --source=file/auto)")
	pf.StringVarP(&f.output, "output", "o", f.output, "Output format: text, json, yaml")
	pf.StringVar(&f.account, "account", f.account, "Account scope: AWS profile, Azure subscription, GCP project, or OCI compartment OCID")
	pf.StringSliceVar(&f.regions, "regions", f.regions, "Limit discovery to these cloud regions")
	pf.BoolVar(&f.allRegions, "all-regions", f.allRegions, "Scan every enabled region (AWS)")
	pf.BoolVar(&f.org, "org", f.org, "Scan the whole organization / all accounts where supported")
	pf.BoolVar(&f.countStandby, "count-standby", f.countStandby, "Count HA standbys / Multi-AZ mirrors as billable nodes")
	pf.BoolVar(&f.includeNonData, "include-non-data", f.includeNonData, "Include non-data nodes (e.g. dedicated masters) in the memory total")
	pf.DurationVar(&f.timeout, "timeout", f.timeout, "Timeout for each external call")
	pf.Float64Var(&f.rateProd, "kubedb-rate-prod", f.rateProd, "KubeDB production rate in USD per GiB per month (from your AppsCode contract)")
	pf.Float64Var(&f.rateNonProd, "kubedb-rate-nonprod", f.rateNonProd, "KubeDB non-production rate in USD per GiB per month")
	pf.BoolVar(&f.prod, "prod", f.prod, "Treat the estate as production (uses the prod rate and the 100 GiB minimum)")

	for _, p := range compare.AllProviders {
		cmd.AddCommand(newCompareProviderCmd(p, f))
	}
	cmd.AddCommand(newCompareAllCmd(f))
	cmd.AddCommand(newCompareOperatorsCmd(clientGetter, f))
	return cmd
}

// newCompareOperatorsCmd scans the current cluster for databases managed by
// alternative (non-KubeDB) operators and reports the KubeDB cost to manage them.
func newCompareOperatorsCmd(clientGetter genericclioptions.RESTClientGetter, f *compareFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "operators",
		Short: "Discover databases run by alternative Kubernetes operators and compare against KubeDB",
		Long: `Scan the current cluster for self-hosted databases and report the KubeDB cost
to manage them. Two sources are detected, purely via the controller-runtime
client and unstructured objects (no dependency on any of these projects):

  - databases managed by alternative operators (CloudNativePG, Zalando, Percona,
    Strimzi, ECK, Altinity, and more), detected by their CRDs; and
  - databases deployed from Bitnami, Chainguard or Docker Hardened Images,
    detected by the container image on StatefulSets and Deployments.

Respects -n/--namespace; defaults to all namespaces.`,
		DisableAutoGenTag: true,
		Args:              cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := clientGetter.ToRESTConfig()
			if err != nil {
				return err
			}
			namespace, _ := cmd.Flags().GetString("namespace")
			dbs, warnings, err := compare.DiscoverOperators(cmd.Context(), cfg, namespace)
			if err != nil {
				return err
			}
			imgDBs, imgWarnings, err := compare.DiscoverImageWorkloads(cmd.Context(), cfg, namespace)
			if err != nil {
				return err
			}
			dbs = append(dbs, imgDBs...)
			warnings = append(warnings, imgWarnings...)

			report := compare.BuildReport("operators", dbs, f.pricing(), warnings)
			report.SelfHosted = true
			return compare.Render(os.Stdout, report, f.output)
		},
	}
}

func newCompareProviderCmd(p compare.Provider, f *compareFlags) *cobra.Command {
	sub := &cobra.Command{
		Use:               string(p),
		Short:             "Compare " + p.DisplayName() + " managed databases against KubeDB",
		DisableAutoGenTag: true,
		Args:              cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCompare(cmd.Context(), []compare.Provider{p}, string(p), f)
		},
	}
	addProviderCredFlags(sub, p, f)
	return sub
}

func newCompareAllCmd(f *compareFlags) *cobra.Command {
	sub := &cobra.Command{
		Use:               "all",
		Short:             "Compare databases across every configured provider and aggregate",
		Long:              "Runs live discovery for every provider and aggregates the results. Providers that are not configured or reachable are reported as warnings rather than errors.",
		DisableAutoGenTag: true,
		Args:              cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCompare(cmd.Context(), compare.AllProviders, "all", f)
		},
	}
	for _, p := range compare.AllProviders {
		addProviderCredFlags(sub, p, f)
	}
	return sub
}

func addProviderCredFlags(cmd *cobra.Command, p compare.Provider, f *compareFlags) {
	switch p {
	case compare.ProviderAtlas:
		cmd.Flags().StringVar(&f.atlas.ClientID, "atlas-client-id", "", "MongoDB Atlas service-account client id")
		cmd.Flags().StringVar(&f.atlas.ClientSecret, "atlas-client-secret", "", "MongoDB Atlas service-account client secret")
		cmd.Flags().StringVar(&f.atlas.OrgID, "atlas-org-id", "", "MongoDB Atlas organization id (optional)")
		cmd.Flags().StringVar(&f.atlas.BaseURL, "atlas-base-url", "", "MongoDB Atlas API base URL (optional)")
	case compare.ProviderElastic:
		cmd.Flags().StringVar(&f.elastic.APIKey, "elastic-api-key", "", "Elastic Cloud API key")
		cmd.Flags().StringVar(&f.elastic.BaseURL, "elastic-base-url", "", "Elastic Cloud API base URL (optional)")
	case compare.ProviderClickHouse:
		cmd.Flags().StringVar(&f.clickhouse.KeyID, "clickhouse-key-id", "", "ClickHouse Cloud API key id")
		cmd.Flags().StringVar(&f.clickhouse.KeySecret, "clickhouse-key-secret", "", "ClickHouse Cloud API key secret")
		cmd.Flags().StringVar(&f.clickhouse.OrgID, "clickhouse-org-id", "", "ClickHouse Cloud organization id (optional)")
		cmd.Flags().StringVar(&f.clickhouse.BaseURL, "clickhouse-base-url", "", "ClickHouse Cloud API base URL (optional)")
	}
}

func runCompare(ctx context.Context, providers []compare.Provider, scope string, f *compareFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var (
		all      []compare.ManagedDatabase
		warnings []string
		hardErr  error
	)
	single := len(providers) == 1
	for _, p := range providers {
		d, err := compare.DiscovererFor(p)
		if err != nil {
			return err
		}
		dbs, warns, err := d.Discover(ctx, f.options())
		if err != nil {
			if single {
				hardErr = err
				break
			}
			warnings = append(warnings, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		all = append(all, dbs...)
		warnings = append(warnings, warns...)
	}
	if hardErr != nil {
		return hardErr
	}

	report := compare.BuildReport(scope, all, f.pricing(), warnings)
	return compare.Render(os.Stdout, report, f.output)
}
