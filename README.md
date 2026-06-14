[![PkgGoDev](https://pkg.go.dev/badge/kubeops.dev/resource-calculator)](https://pkg.go.dev/kubeops.dev/resource-calculator)
[![Build Status](https://github.com/kubeops/resource-calculator/workflows/CI/badge.svg)](https://github.com/kubeops/resource-calculator/actions?workflow=CI)
[![Slack](https://shields.io/badge/Join_Slack-salck?color=4A154B&logo=slack)](https://slack.appscode.com)
[![Twitter](https://img.shields.io/twitter/follow/appscodehq.svg?style=social&logo=twitter&label=Follow)](https://twitter.com/intent/follow?screen_name=AppsCodeHQ)

# resource-calculator

Kubernetes resource metrics calculator, plus an `inspect` command that lists
managed cloud databases with their allocated CPU and memory.

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
- `inspect`: list managed cloud databases with their allocated CPU and memory
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

## inspect: list managed databases with allocated CPU and memory

`inspect` inventories the managed databases on a public cloud account or a DBaaS
organization and lists each one with its allocated CPU and memory, plus the
estate totals.

Every managed database is reduced to its allocated CPU and memory per node times
its node count (counted as `replicas x size per replica`). A 3 replica
PostgreSQL with 8 GiB per replica counts as 24 GiB. `inspect` lists each
discovered database with that allocation and sums the whole estate.

Supported providers: `aws`, `azure`, `gcp`, `oci`, `atlas` (MongoDB Atlas),
`elastic` (Elastic Cloud), `clickhouse` (ClickHouse Cloud), and `all`.

### Quick start

Live, using the provider's official SDK and your existing cloud credentials:

```bash
# AWS: default credential chain, every enabled region
resource-calculator inspect aws --all-regions

# GCP: a specific project
resource-calculator inspect gcp --account=my-project

# MongoDB Atlas: service account
resource-calculator inspect atlas \
  --atlas-client-id=$ATLAS_CLIENT_ID --atlas-client-secret=$ATLAS_CLIENT_SECRET
```

Offline, from exported JSON (no credentials, handy for CI or sharing):

```bash
resource-calculator inspect aws --source=file --from-file=aws-bundle.json -o json
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
documented in [docs/inspect.md](docs/inspect.md).

### Self-hosted operators

`inspect operators` scans the current cluster for self-hosted databases and
lists their allocated CPU and memory. It finds databases run by alternative
(non-KubeDB) operators (CloudNativePG, Zalando, Percona, Strimzi, ECK, Altinity,
and more), detected by their CRDs, and databases deployed from Bitnami,
Chainguard or Docker Hardened Images, detected by container image. Both use the
controller-runtime client and unstructured objects, with no dependency on those
projects:

```bash
resource-calculator inspect operators
```

See [docs/inspect.md](docs/inspect.md) for the full operator list and behavior.

### Output

The text output is a table with one row per database (columns include provider,
service, engine, name, region, node type, CPU and memory per node, node count,
and the per-database totals), ending with a totals line that reports the count
of databases and nodes and the total vCPU and memory allocated across the
estate. For cloud providers an estimated managed monthly cost is shown as an
informational column: a memory-normalized list-price estimate, labelled as an
estimate. Use `-o json` or `-o yaml` for the full report.

### Useful flags

| flag | meaning |
| --- | --- |
| `--account` | AWS profile / Azure subscription / GCP project / OCI compartment OCID |
| `--all-regions` | scan every enabled region (AWS) |
| `--org` | organization / all-accounts scan where supported (AWS) |
| `--count-standby` | count HA standbys / Multi-AZ mirrors as nodes (default true) |
| `--include-non-data` | include non-data nodes (for example OpenSearch dedicated masters) |
| `-o, --output` | `text` (default), `json`, or `yaml` |

Totals show the database and node counts and the total vCPU and memory
allocated. Full reference, including per-provider credentials, the JSON bundle
formats, and node-counting rules: [docs/inspect.md](docs/inspect.md).
