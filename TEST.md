# Testing

How to build, test, and lint `resource-calculator`, and how to exercise the
`compare` command without cloud credentials.

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
go test -mod=vendor ./pkg/compare/...                  # the compare package
go test -mod=vendor ./pkg/compare/ -run TestAtlas      # a single test
go test -mod=vendor -v ./pkg/compare/ -run TestParse   # verbose, by prefix
```

Note: `make fmt` import-grouping uses `reimport3.py`, which only exists in the
build image. Run `make fmt` (not a bare `gofmt`) before committing if you touch
imports.

## What the unit tests cover

`pkg/compare/compare_test.go` is pure and offline (no network, no CLI, no cloud
credentials). It covers the parts that carry the logic:

- Sizing catalogs: `awsInstanceSpec`, `azureFlexSpec`, `gcpCloudSQLSpec`,
  `atlasSpec`, `ociMySQLSpec` (class / SKU / tier to vCPU and memory).
- Pricing and savings: the production 100 GiB floor and `BuildReport`
  aggregation (totals, current spend, KubeDB cost, savings percent, sort order).
- Every provider parser: `parseAWSRDSInstances` (incl. Multi-AZ standby and
  serverless exclusion), `parseAWSElastiCacheReplicationGroups`,
  `parseAzureFlex`, `parseGCPCloudSQL`, `parseOCIMySQL`, `parseAtlasClusters`,
  `parseElasticDeployment`, `parseClickHouseServices`.

When you add a provider or a sizing entry, add a parser test and a catalog test
in the same file.

## Testing `compare` offline (file mode)

The file source parses the same JSON the cloud CLIs emit, so you can exercise
discovery end to end without any credentials. A bundle is a JSON object keyed by
collector (full key list per provider in `docs/compare.md`).

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

resource-calculator compare aws --source=file --from-file=aws.json \
  --prod --kubedb-rate-prod=8            # text
resource-calculator compare aws --source=file --from-file=aws.json -o json
```

Quick checks that the math is right: a Multi-AZ `db.r6g.xlarge` (32 GiB) counts
as 2 nodes / 64 GiB; a 3-node `cache.r7g.large` (16 GiB) counts as 48 GiB.

`compare all` with no credentials is also a safe smoke test: it attempts every
provider and reports the ones it cannot reach as warnings instead of failing.

## Testing `compare` live (manual, needs credentials)

Live discovery cannot be unit-tested, since it calls real cloud APIs. Smoke-test
each source with real credentials:

```bash
resource-calculator compare aws   --source=sdk --regions=us-east-1 --kubedb-rate-prod=8
resource-calculator compare aws   --source=cli --all-regions       --kubedb-rate-prod=8
resource-calculator compare atlas --source=rest --atlas-client-id=... --atlas-client-secret=... --kubedb-rate-nonprod=6
```

Without credentials the SDK and REST paths surface a clear per-service warning
and produce an empty report rather than crashing; that is the expected
"unconfigured" behavior. Required credentials and scope flags per provider are
in `docs/compare.md`.

## Linting notes

`golangci-lint` includes `unparam`, which flags always-nil return values and
unused parameters. Keep helper signatures tight: do not return a `warnings`
slice that is always `nil`, and drop unused `ctx`/`opts` parameters (for
example, the Elastic SDK calls are not context aware). Linting the heavy command
package can be memory hungry; run it with `--concurrency=1` if it is killed.
