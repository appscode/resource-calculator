# resource-calculator inspect

`inspect` lists the managed database services running on a public cloud account
(or a DBaaS organization), shows the CPU and memory allocated to each database
server, and prints the estate totals.

Every managed database is reduced to its allocated CPU and memory per node times
its node count (counted as `replicas x size per replica`). A 3 replica
PostgreSQL with 8 GiB per replica counts as 24 GiB. `inspect` lists each
discovered database with that allocation and sums the whole estate.

Supported providers: `aws`, `azure`, `gcp`, `oci`, `atlas` (MongoDB Atlas),
`elastic` (Elastic Cloud), `clickhouse` (ClickHouse Cloud), plus `all`. The
`operators` subcommand instead scans the current cluster for self-hosted
databases run by alternative operators or deployed from vendor images (see
"Inspecting self-hosted operators").

## Quick start

Offline, from exported JSON (no credentials needed):

```bash
aws rds describe-db-instances --output json > rds.json
# wrap the outputs into a bundle (see "File bundles" below), then:
resource-calculator inspect aws --source=file --from-file=aws-bundle.json
```

Live, using your already-configured cloud CLI:

```bash
resource-calculator inspect aws --all-regions
```

Live DBaaS (REST):

```bash
resource-calculator inspect atlas \
  --atlas-client-id=$ATLAS_CLIENT_ID --atlas-client-secret=$ATLAS_CLIENT_SECRET
```

## Discovery sources (`--source`)

Discovery is layered. The priority order is sdk, cli, rest, file:

- `auto` (default): use `--from-file` when set, otherwise the preferred live
  source. That is the official SDK for AWS, Azure, GCP, OCI, Atlas and Elastic,
  and REST for ClickHouse.
- `sdk`: use the vendor's official Go SDK. Built in for AWS, Azure, GCP, OCI,
  Atlas and Elastic. ClickHouse Cloud has no official Go control-plane SDK, so
  `--source=sdk` is rejected for it (use `rest` or `file`). See Credentials
  below for what each SDK reads.
- `cli`: shell out to the provider's installed CLI (`aws`, `az`, `gcloud`,
  `oci`). Reuses your existing profiles, SSO sessions and assume-role config.
- `rest`: call the vendor REST control-plane API directly (Atlas, Elastic,
  ClickHouse).
- `file`: parse previously exported JSON. No live calls, no credentials.

### Credentials (for `--source=sdk` and `auto`)

| Provider | Auth used by the SDK | Scope flag |
| --- | --- | --- |
| AWS | default credential chain (env, shared config, SSO, instance role) | `--account` = profile; `--org` assumes `OrganizationAccountAccessRole` per member account; `--all-regions` |
| Azure | `DefaultAzureCredential` (env, managed identity, `az login`); Resource Graph covers every readable subscription | `--account` = subscription (optional) |
| GCP | Application Default Credentials | `--account` = project (required) |
| OCI | `~/.oci/config` or instance principals | `--account` = compartment/tenancy OCID (required) |
| MongoDB Atlas | service account OAuth2 | `--atlas-client-id`, `--atlas-client-secret`, `--atlas-org-id` |
| Elastic Cloud | API key | `--elastic-api-key` |
| ClickHouse Cloud | REST (HTTP Basic), no SDK | `--clickhouse-key-id`, `--clickhouse-key-secret` |

## Output

The default text output is a table with one row per discovered database. The
columns are: PROVIDER, SERVICE, ENGINE, NAME, REGION, NODE TYPE, CPU/NODE,
MEM/NODE, NODES, TOTAL CPU, TOTAL MEM, EST. $/MO. It ends with a totals line:

```
Total: N databases, M nodes, X vCPU, Y GiB memory allocated
```

TOTAL CPU and TOTAL MEM are the per-database allocation (`per node x node
count`); the totals line sums them across the estate. For cloud providers the
EST. $/MO column shows an estimated managed monthly cost as an informational
figure: a memory-normalized list-price estimate (us region, on-demand), flagged
as estimated, useful for an order-of-magnitude reference, not a bill.

Use `-o json` or `-o yaml` for the full report (the same database list and
totals) in a form suitable for piping into other tooling.

Flags:

| flag | meaning |
| --- | --- |
| `--count-standby` | count HA standbys / Multi-AZ mirrors as nodes (default true) |
| `--include-non-data` | include non-data nodes (for example OpenSearch dedicated masters) |
| `-o, --output` | `text` (default), `json`, or `yaml` |

## Node counting

`inspect` counts every node so the CPU and memory totals reflect the full
allocation of the estate:

- AWS RDS: each instance is one node; a Multi-AZ standby adds one (with
  `--count-standby`). Aurora and DocumentDB cluster members are each counted.
  Read replicas are counted as their own instances. `db.serverless` is excluded
  (no fixed memory).
- ElastiCache / MemoryDB: every node across all shards and replicas.
- Azure flexible servers: HA standby adds one node; read replicas are separate
  resources. Redis: `shardCount x (1 + replicas)`.
- GCP Cloud SQL: REGIONAL availability adds an HA standby; read replicas are
  separate. Memorystore: `1 + replicaCount`. AlloyDB read pools: per node.
- OCI MySQL HeatWave: HA is 3 nodes. Base Database: `nodeCount` (RAC).
- Atlas: electable + read-only + analytics nodes across every shard and region.
- Elastic Cloud: each topology tier counts `size per zone x zone_count`; only
  data tiers unless `--include-non-data`.
- ClickHouse Cloud: `memory per replica x replica count`; stopped services are
  skipped.

Throughput or serverless services that have no meaningful "memory allocated to
database servers" figure are excluded from the totals (Aurora Serverless,
Cosmos DB RU, Spanner, Bigtable, DynamoDB, OCI NoSQL, and similar).

## File bundles (`--source=file`)

A bundle is a single JSON object that maps a collector key to that command's raw
JSON output. Only the keys you include are parsed.

### AWS (`aws ... --output json`)

| key | command |
| --- | --- |
| `rds-instances` | `aws rds describe-db-instances` |
| `elasticache-replication-groups` | `aws elasticache describe-replication-groups` |
| `elasticache-cache-clusters` | `aws elasticache describe-cache-clusters` |
| `memorydb-clusters` | `aws memorydb describe-clusters --show-shard-details` |
| `opensearch-domains` | `aws opensearch describe-domains --domain-names ...` |

```json
{
  "rds-instances": { "DBInstances": [ { "DBInstanceIdentifier": "prod-pg", "DBInstanceClass": "db.r6g.xlarge", "Engine": "postgres", "MultiAZ": true } ] },
  "elasticache-replication-groups": { "ReplicationGroups": [ { "ReplicationGroupId": "cache1", "CacheNodeType": "cache.r7g.large", "MemberClusters": ["a","b","c"] } ] }
}
```

### Azure (`az ... -o json`)

Keys: `postgres-flexible` (`az postgres flexible-server list`),
`mysql-flexible` (`az mysql flexible-server list`), `redis` (`az redis list`),
`mongo-vcore` (`az cosmosdb mongocluster list`). Each value is the array the
command returns.

### GCP (`gcloud ... --format=json`)

Keys: `cloudsql-instances` (`gcloud sql instances list`), `memorystore-redis`
(`gcloud redis instances list --region=-`), `alloydb-instances`
(`gcloud alloydb instances list --region=- --cluster=-`).

### OCI (`oci ... --output json`)

Keys: `mysql-db-systems`, `base-db-systems`, `autonomous-databases`,
`cache-clusters`. Each value is the `{ "data": [ ... ] }` object the CLI
returns.

### Atlas / Elastic / ClickHouse

- Atlas key `clusters`: the `{ "results": [ ... ] }` cluster-list response.
- Elastic key `deployments`: an array of deployment detail objects (the
  `GET /api/v1/deployments/{id}` shape), or `{ "deployments": [ ... ] }`.
- ClickHouse key `services`: the `{ "result": [ ... ] }` services response.

## Organization and multi-region scans

- AWS: `--all-regions` scans every enabled region. For a whole organization,
  scan each member account with `--account=<profile>` (configure an
  assume-role profile per account, for example via
  `OrganizationAccountAccessRole`).
- Azure: pass the subscription with `--account`.
- GCP: pass the project with `--account`.
- OCI: `--account=<compartment-or-tenancy-OCID>` is required; list calls are
  compartment scoped.

## Sizing accuracy

Instance class, SKU and tier sizes are resolved from bundled lookup tables and
parsers (for example AWS `db.*`/`cache.*` map to the underlying EC2 family
memory, GCP `db-custom-CPU-MEMMB` encodes memory in the name, Atlas M-tiers map
to published RAM). Unknown classes are reported with a warning and excluded from
the totals rather than guessed.

## Inspecting self-hosted operators (`inspect operators`)

`inspect operators` scans the current cluster (your kubeconfig context) for
self-hosted databases managed by alternative (non-KubeDB) operators and reports
their allocated CPU and memory. It detects each operator purely by the presence
of its CRD (group/version/kind), using the controller-runtime client with
unstructured objects, so the binary takes no build dependency on any of these
projects. Operators whose CRDs are absent are skipped.

It also detects databases deployed from vendor images rather than an operator,
by inspecting the container images on StatefulSets and Deployments: Bitnami
(`docker.io/bitnami/*`, legacy `bitnamilegacy/*`), Chainguard (`cgr.dev/.../*`)
and Docker Hardened Images (`dhi.io/*`). Only these three image families are
matched, so operator-managed and upstream-official images are not double
counted, and metrics sidecars (`*-exporter`) are ignored.

```bash
resource-calculator inspect operators
resource-calculator inspect operators -n team-a -o json   # scope to one namespace
```

It reads the pod CPU and memory limit (falling back to the request) and the
replica/size field from each CR, multiplies them, and groups the result by
operator (or vendor for image-deployed databases). The text output is a
per-operator/vendor breakdown with the columns: OPERATOR / VENDOR, DATABASES,
NODES, TOTAL CPU, TOTAL MEM, LICENSING.

Detected operators include:

- PostgreSQL: CloudNativePG, Zalando, StackGres, Percona
- MySQL / MariaDB: Percona XtraDB, Oracle MySQL Operator, MOCO, Bitpoke, mariadb-operator
- MongoDB: Percona Server for MongoDB, MongoDB Community Operator
- Redis: Spotahome, OpsTree, DragonflyDB, Redis Enterprise
- Search: Elastic ECK, OpenSearch Operator, Apache Solr Operator
- Streaming: Strimzi (Kafka), RabbitMQ Cluster Operator
- Analytics: Altinity ClickHouse
- Cassandra: cass-operator (K8ssandra), Scylla
- Other: Hazelcast
- Image-deployed (matched by container image): Bitnami, Bitnami (legacy),
  Chainguard, Docker Hardened Images

Notes:

- CPU and memory come from the CR's pod resource limit/request. A CR without
  resource limits is listed with a warning and zero allocation (it cannot be
  sized).
- `--namespace`/`-n` scopes the scan; the default is all namespaces.
- To add an operator, add a descriptor (GVK + extractor) to
  `pkg/compare/operators.go`. No new module dependency is required because
  detection and reading are done through unstructured objects.

## Implementation notes

Each provider's official SDK discoverer lives in
`pkg/compare/<provider>_sdk.go` and reuses the same sizing helpers as the CLI,
REST and file paths, so every source yields identical results.
`google.golang.org/api` is pinned to a release compatible with the repo's Go
version. ClickHouse Cloud has no official Go control-plane SDK and uses REST.
See [DESIGN.md](../DESIGN.md) for the full architecture.
