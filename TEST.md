# Testing

How to build, test, and lint `resource-calculator`, and how to exercise the
`inspect` command without cloud credentials.

## Canonical commands (build image)

Every target runs inside the pinned `ghcr.io/appscode/golang-dev:1.25`
container via the Makefile, with vendor mode forced. Prefer these over running
`go` directly so you match CI.

```bash
make test       # go test ./pkg/... (alias: unit-tests)
make lint       # golangci-lint (.golangci.yml: default linters + unparam)
make build      # bin/resource-calculator-<os>-<arch>
make verify     # verify-gen + verify-modules (go mod tidy && go mod vendor must be clean)
make ci         # verify check-license lint build unit-tests (what GitHub Actions runs)
```

`make ci` is the gate to reproduce before opening a PR.

## Running tests directly

If you have a local Go toolchain matching the `go` directive in `go.mod`
(`go 1.25.5`), you can run tests without docker. Vendor mode is required.

```bash
go test -mod=vendor ./pkg/...                          # all packages
go test -mod=vendor ./pkg/inspect/...                  # the inspect engine package
go test -mod=vendor ./pkg/inspect/ -run TestAtlas      # a single test
go test -mod=vendor -v ./pkg/inspect/ -run TestParse   # verbose, by prefix
```

Note: `make fmt` import-grouping uses `reimport3.py`, which only exists in the
build image. Run `make fmt` (not a bare `gofmt`) before committing if you touch
imports.

## What the unit tests cover

`pkg/inspect/inspect_test.go` is pure and offline (no network, no CLI, no cloud
credentials). It covers the parts that carry the logic:

- Sizing catalogs: `awsInstanceSpec`, `azureFlexSpec`, `gcpCloudSQLSpec`,
  `atlasSpec`, `ociMySQLSpec` (class / SKU / tier to vCPU and memory).
- `BuildReport` aggregation: the estate totals (database count, node count,
  total vCPU, total memory) and the footprint sort order. (The earlier pricing
  test was removed along with the pricing model.)
- Every provider parser: `parseAWSRDSInstances` (incl. Multi-AZ standby and
  serverless exclusion), `parseAWSElastiCacheReplicationGroups`,
  `parseAzureFlex`, `parseGCPCloudSQL`, `parseOCIMySQL`, `parseAtlasClusters`,
  `parseElasticDeployment`, `parseClickHouseServices`.
- Operator extractors (`inspect operators`): `TestOperatorExtract*` run each
  operator descriptor's extractor on a sample CR (CloudNativePG, Strimzi's
  kafka+zookeeper components, Percona replsets, and the unknown-memory note).
- Image classifier: `TestClassifyDBImage` maps Bitnami / Chainguard / Docker
  Hardened Image references to (vendor, engine) and rejects `*-exporter`
  sidecars and untargeted (operator / upstream) images.

When you add a cloud provider or sizing entry, add a parser test and a catalog
test. When you add an operator or image vendor, add an extractor/classifier test
in the same file.

## Testing `inspect kubedb` (cluster scan)

`inspect kubedb` shares its discovery path with `calculate`, so the catalog and
version selection are exercised by the same code. The command itself needs a
cluster; point your kubeconfig at a throwaway cluster (for example kind),
install a couple of workloads (a Deployment or a KubeDB resource), and run:

```bash
resource-calculator inspect kubedb                                # table
resource-calculator inspect kubedb --apiGroups=kubedb.com -o json # filter + JSON
resource-calculator inspect kubedb --all -o yaml                  # every kubeconfig context
```

Each row reports group/kind, namespace, name, UID, age and memory limit. Kinds
with no `memory` entry in `AppResourceLimits` print `0` for memory; with no
reachable cluster the command returns the client/config error. `-o/--output` is
the persistent flag from `inspect`, so it can sit before or after `kubedb`.

## Testing `inspect` offline (file mode)

The file source parses the same JSON the cloud CLIs emit, so you can exercise
discovery end to end without any credentials. A bundle is a JSON object keyed by
collector (full key list per provider in `docs/inspect.md`).

```bash
cat > aws.json <<'JSON'
{
  "rds-instances": { "DBInstances": [
    { "DBInstanceIdentifier": "prod-pg", "DBInstanceClass": "db.r6g.xlarge", "Engine": "postgres", "MultiAZ": true }
  ] },
  "elasticache-replication-groups": { "ReplicationGroups": [
    { "ReplicationGroupId": "cache1", "CacheNodeType": "cache.r7g.large", "MemberClusters": ["a","b","c"] }
  ] }
}
JSON

resource-calculator inspect aws --source=file --from-file=aws.json            # text
resource-calculator inspect aws --source=file --from-file=aws.json -o json
```

Quick checks that the math is right: a Multi-AZ `db.r6g.xlarge` (32 GiB) counts
as 2 nodes / 64 GiB allocated; a 3-node `cache.r7g.large` (16 GiB) counts as 48
GiB. The totals line sums the vCPU and memory across the listed databases.

`inspect all` with no credentials is also a safe smoke test: it attempts every
provider and reports the ones it cannot reach as warnings instead of failing.

## Testing `inspect` live (manual, needs credentials)

Live discovery cannot be unit-tested, since it calls real cloud APIs. Smoke-test
each source with real credentials:

```bash
resource-calculator inspect aws   --source=sdk --regions=us-east-1
resource-calculator inspect aws   --source=cli --all-regions
resource-calculator inspect atlas --source=rest --atlas-client-id=... --atlas-client-secret=...
```

Without credentials the SDK and REST paths surface a clear per-service warning
and produce an empty report rather than crashing; that is the expected
"unconfigured" behavior. Required credentials and scope flags per provider are
in `docs/inspect.md`.

## Testing `inspect operators` (cluster scan)

The operator and image-detection logic is unit-tested offline (the operator
extractors and image classifier above). The live scan needs a cluster:

```bash
resource-calculator inspect operators
resource-calculator inspect operators -n team-a -o json   # scope to one namespace
```

To exercise it end to end, point your kubeconfig at a throwaway cluster (for
example kind), install an operator or a Bitnami/Chainguard chart, and run the
command. Operators whose CRDs are absent are skipped; CRs and workloads without
a memory limit/request are listed with a warning and zero memory; with no
reachable cluster the command returns the client/config error.

Single-test runs for the offline logic:

```bash
go test -mod=vendor ./pkg/inspect/ -run TestOperatorExtract
go test -mod=vendor ./pkg/inspect/ -run TestClassifyDBImage
```

## Linting notes

`golangci-lint` includes `unparam`, which flags always-nil return values and
unused parameters. Keep helper signatures tight: do not return a `warnings`
slice that is always `nil`, and drop unused `ctx`/`opts` parameters (for
example, the Elastic SDK calls are not context aware). Linting the heavy command
package can be memory hungry; run it with `--concurrency=1` if it is killed.
