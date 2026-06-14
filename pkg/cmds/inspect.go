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

	"kubeops.dev/resource-calculator/pkg/inspect"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// inspectFlags holds the flags shared by every `inspect` subcommand plus the
// per-vendor credentials used by the DBaaS providers.
type inspectFlags struct {
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

	atlas      inspect.AtlasCreds
	elastic    inspect.ElasticCreds
	clickhouse inspect.ClickHouseCreds
}

func (f *inspectFlags) options() inspect.Options {
	return inspect.Options{
		Source:         inspect.Source(f.source),
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

// NewCmdInspect builds the `inspect` command tree: one subcommand per supported
// managed-database provider, `all`, `operators` (in-cluster self-hosted scan),
// and `kubedb` (per-object listing of KubeDB-managed resources in the cluster).
// Each lists the discovered databases with their allocated CPU and memory and
// totals (per-object memory limits for `kubedb`).
func NewCmdInspect(clientGetter genericclioptions.RESTClientGetter) *cobra.Command {
	f := &inspectFlags{
		source:       string(inspect.SourceAuto),
		output:       "text",
		countStandby: true,
		timeout:      60 * time.Second,
	}

	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Inspect databases on clouds, DBaaS or the cluster and list their CPU and memory",
		Long: `Discover databases and list each one's allocated CPU and memory, with totals.

Targets: a cloud provider or DBaaS (inspect aws|azure|gcp|oci|atlas|elastic|
clickhouse), all of them (inspect all), the current cluster's self-hosted
databases run by alternative operators and Bitnami/Chainguard/Docker Hardened
Images (inspect operators), or KubeDB-managed resources in the current cluster
listed one row per object (inspect kubedb).

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
	pf.BoolVar(&f.countStandby, "count-standby", f.countStandby, "Count HA standbys / Multi-AZ mirrors as separate nodes")
	pf.BoolVar(&f.includeNonData, "include-non-data", f.includeNonData, "Include non-data nodes (e.g. dedicated masters) in the totals")
	pf.DurationVar(&f.timeout, "timeout", f.timeout, "Timeout for each external call")

	for _, p := range inspect.AllProviders {
		cmd.AddCommand(newInspectProviderCmd(p, f))
	}
	cmd.AddCommand(newInspectAllCmd(f))
	cmd.AddCommand(newInspectOperatorsCmd(clientGetter, f))
	cmd.AddCommand(newInspectKubeDBCmd(clientGetter, f))
	return cmd
}

// newInspectOperatorsCmd scans the current cluster for self-hosted databases and
// lists their allocated CPU and memory.
func newInspectOperatorsCmd(clientGetter genericclioptions.RESTClientGetter, f *inspectFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "operators",
		Short: "Inspect self-hosted databases (alternative operators + Bitnami/Chainguard/Docker Hardened Images)",
		Long: `Scan the current cluster for self-hosted databases and list their allocated CPU
and memory. Two sources are detected, purely via the controller-runtime client
and unstructured objects (no dependency on any of these projects):

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
			dbs, warnings, err := inspect.DiscoverOperators(cmd.Context(), cfg, namespace)
			if err != nil {
				return err
			}
			imgDBs, imgWarnings, err := inspect.DiscoverImageWorkloads(cmd.Context(), cfg, namespace)
			if err != nil {
				return err
			}
			dbs = append(dbs, imgDBs...)
			warnings = append(warnings, imgWarnings...)

			report := inspect.BuildReport("operators", dbs, warnings)
			report.SelfHosted = true
			return inspect.Render(os.Stdout, report, f.output)
		},
	}
}

func newInspectProviderCmd(p inspect.Provider, f *inspectFlags) *cobra.Command {
	sub := &cobra.Command{
		Use:               string(p),
		Short:             "Inspect " + p.DisplayName() + " managed databases (CPU, memory, totals)",
		DisableAutoGenTag: true,
		Args:              cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInspect(cmd.Context(), []inspect.Provider{p}, string(p), f)
		},
	}
	addProviderCredFlags(sub, p, f)
	return sub
}

func newInspectAllCmd(f *inspectFlags) *cobra.Command {
	sub := &cobra.Command{
		Use:               "all",
		Short:             "Inspect databases across every configured provider and aggregate",
		Long:              "Runs live discovery for every provider and aggregates the results. Providers that are not configured or reachable are reported as warnings rather than errors.",
		DisableAutoGenTag: true,
		Args:              cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInspect(cmd.Context(), inspect.AllProviders, "all", f)
		},
	}
	for _, p := range inspect.AllProviders {
		addProviderCredFlags(sub, p, f)
	}
	return sub
}

func addProviderCredFlags(cmd *cobra.Command, p inspect.Provider, f *inspectFlags) {
	switch p {
	case inspect.ProviderAtlas:
		cmd.Flags().StringVar(&f.atlas.ClientID, "atlas-client-id", "", "MongoDB Atlas service-account client id")
		cmd.Flags().StringVar(&f.atlas.ClientSecret, "atlas-client-secret", "", "MongoDB Atlas service-account client secret")
		cmd.Flags().StringVar(&f.atlas.OrgID, "atlas-org-id", "", "MongoDB Atlas organization id (optional)")
		cmd.Flags().StringVar(&f.atlas.BaseURL, "atlas-base-url", "", "MongoDB Atlas API base URL (optional)")
	case inspect.ProviderElastic:
		cmd.Flags().StringVar(&f.elastic.APIKey, "elastic-api-key", "", "Elastic Cloud API key")
		cmd.Flags().StringVar(&f.elastic.BaseURL, "elastic-base-url", "", "Elastic Cloud API base URL (optional)")
	case inspect.ProviderClickHouse:
		cmd.Flags().StringVar(&f.clickhouse.KeyID, "clickhouse-key-id", "", "ClickHouse Cloud API key id")
		cmd.Flags().StringVar(&f.clickhouse.KeySecret, "clickhouse-key-secret", "", "ClickHouse Cloud API key secret")
		cmd.Flags().StringVar(&f.clickhouse.OrgID, "clickhouse-org-id", "", "ClickHouse Cloud organization id (optional)")
		cmd.Flags().StringVar(&f.clickhouse.BaseURL, "clickhouse-base-url", "", "ClickHouse Cloud API base URL (optional)")
	}
}

func runInspect(ctx context.Context, providers []inspect.Provider, scope string, f *inspectFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var (
		all      []inspect.ManagedDatabase
		warnings []string
		hardErr  error
	)
	single := len(providers) == 1
	for _, p := range providers {
		d, err := inspect.DiscovererFor(p)
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

	report := inspect.BuildReport(scope, all, warnings)
	return inspect.Render(os.Stdout, report, f.output)
}
