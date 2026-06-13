[![PkgGoDev](https://pkg.go.dev/badge/kubeops.dev/resource-calculator)](https://pkg.go.dev/kubeops.dev/resource-calculator)
[![Build Status](https://github.com/kubeops/resource-calculator/workflows/CI/badge.svg)](https://github.com/kubeops/resource-calculator/actions?workflow=CI)
[![Slack](https://shields.io/badge/Join_Slack-salck?color=4A154B&logo=slack)](https://slack.appscode.com)
[![Twitter](https://img.shields.io/twitter/follow/appscodehq.svg?style=social&logo=twitter&label=Follow)](https://twitter.com/intent/follow?screen_name=AppsCodeHQ)

# resource-calculator

Kubernetes resource metrics calculator, plus a `compare` command that estimates
the savings of migrating managed cloud databases to KubeDB.

## Install

Build the binary with the Makefile (it runs inside the pinned build image):

```bash
make build            # produces bin/resource-calculator-<os>-<arch>
```

The binary runs standalone or as a `kubectl` plugin: put it on your `PATH` named
`kubectl-resource_calculator` and call `kubectl resource-calculator ...`.

## Commands

- `calculate`: sum CPU, memory and storage of workloads in a Kubernetes cluster.
- `convert`: convert KubeDB `v1alpha1` resources to `v1alpha2`.
- `check-deprecated`: list installed KubeDB resources on deprecated versions.
- `compare`: estimate the savings of migrating managed cloud databases to KubeDB
  (see the user guide below).

Architecture and design notes live in [DESIGN.md](DESIGN.md).

## Kubernetes cluster commands

These operate on the cluster in your current kubeconfig context (add `--all` to
sweep every context):

```bash
# sum CPU / memory / storage by kind (-o text | json | yaml)
resource-calculator calculate -o text

# list KubeDB resources still on the v1alpha1 API
resource-calculator check-deprecated

# convert KubeDB v1alpha1 resources to v1alpha2 YAML on disk
resource-calculator convert --dir ./converted
```

## compare: cloud database to KubeDB savings

`compare` inventories the managed databases on a public cloud account or a DBaaS
organization, sums the memory allocated to their database servers, and estimates
how much could be saved by migrating them to KubeDB.

KubeDB is licensed on a single metric: the memory allocated to database
containers, counted as `replicas x memory per replica`. A 3 replica PostgreSQL
with 8 GiB per replica counts as 24 GiB. `compare` mirrors that model exactly:
every managed database is reduced to `memory per node x node count`, summed
across the estate, and priced against current managed spend.

Supported providers: `aws`, `azure`, `gcp`, `oci`, `atlas` (MongoDB Atlas),
`elastic` (Elastic Cloud), `clickhouse` (ClickHouse Cloud), and `all`.

### Quick start

Live, using the provider's official SDK and your existing cloud credentials:

```bash
# AWS: default credential chain, every enabled region
resource-calculator compare aws --all-regions --prod --kubedb-rate-prod=8

# GCP: a specific project
resource-calculator compare gcp --account=my-project --prod --kubedb-rate-prod=8

# MongoDB Atlas: service account
resource-calculator compare atlas \
  --atlas-client-id=$ATLAS_CLIENT_ID --atlas-client-secret=$ATLAS_CLIENT_SECRET \
  --kubedb-rate-nonprod=6
```

Offline, from exported JSON (no credentials, handy for CI or sharing):

```bash
resource-calculator compare aws --source=file --from-file=aws-bundle.json \
  --prod --kubedb-rate-prod=8 -o json
```

### How databases are discovered

`--source` selects the mechanism (priority order sdk, cli, rest, file):

- `sdk` (used by `auto`): the vendor's official Go SDK, built in for AWS, Azure,
  GCP, OCI, Atlas and Elastic. ClickHouse Cloud uses REST (no official Go SDK).
- `cli`: shell out to your installed `aws`/`az`/`gcloud`/`oci` CLI.
- `rest`: call the vendor REST API directly (Atlas, Elastic, ClickHouse).
- `file`: parse exported JSON with `--from-file`. No live calls or credentials.

`auto` (the default) uses `--from-file` when given, otherwise the SDK (REST for
ClickHouse). Credentials per provider and the `--from-file` bundle formats are
documented in [docs/compare.md](docs/compare.md).

### Self-hosted operators

`compare operators` scans the current cluster for self-hosted databases and
reports the KubeDB cost to manage them. It finds databases run by alternative
(non-KubeDB) operators (CloudNativePG, Zalando, Percona, Strimzi, ECK, Altinity,
and more), detected by their CRDs, and databases deployed from Bitnami,
Chainguard or Docker Hardened Images, detected by container image. Both use the
controller-runtime client and unstructured objects, with no dependency on those
projects:

```bash
resource-calculator compare operators --prod --kubedb-rate-prod=8
```

See [docs/compare.md](docs/compare.md) for the full operator list and behavior.

### Pricing and savings

The headline is `current managed spend - KubeDB license cost`.

- Provide the KubeDB rate with `--kubedb-rate-prod` / `--kubedb-rate-nonprod`
  (USD per GiB per month, from your AppsCode contract). The public rate is
  quote-based, so there is no built-in default. `--prod` selects the production
  rate and applies the 100 GiB production minimum.
- Current managed spend is estimated from bundled, memory-normalized list-price
  anchors and is clearly labelled as an estimate. Without a KubeDB rate the
  report still shows the discovered memory footprint and current spend.

### Useful flags

| flag | meaning |
| --- | --- |
| `--account` | AWS profile / Azure subscription / GCP project / OCI compartment OCID |
| `--all-regions` | scan every enabled region (AWS) |
| `--org` | organization / all-accounts scan where supported (AWS) |
| `--count-standby` | count HA standbys / Multi-AZ mirrors as billable nodes (default true) |
| `--include-non-data` | include non-data nodes (for example OpenSearch dedicated masters) |
| `--kubedb-rate-prod` / `--kubedb-rate-nonprod` | KubeDB rate, USD per GiB per month |
| `--prod` | use the production rate and the 100 GiB minimum |
| `-o, --output` | `text` (default), `json`, or `yaml` |

Full reference, including per-provider credentials, the JSON bundle formats, and
node-counting rules: [docs/compare.md](docs/compare.md).
