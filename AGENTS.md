# AGENTS.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository. `CLAUDE.md` includes this file via `@AGENTS.md`.

## What this is

`resource-calculator` is a Cobra-based CLI (single binary, `main.go` -> `pkg/cmds`) that is shipped both as a standalone binary and as a `kubectl` plugin.

Subcommands live under `pkg/cmds/`:

- `calculate` -- walks every GVK registered in `kmodules.xyz/resource-metrics` `api.RegisteredTypes()`, lists objects via the dynamic client, sums CPU/memory/storage via `resourcemetrics.AppResourceLimits`, and prints a per-kind table or JSON/YAML. Picks the highest available API version per `GroupKind` using `kmodules.xyz/apiversion`. Supports `--all` to iterate every kubeconfig context.
- `convert` -- converts KubeDB `kubedb/v1alpha1` resources (Elasticsearch, Etcd, MariaDB, Memcached, MongoDB, MySQL, PerconaXtraDB, Postgres, Redis) to `v1alpha2` using the generated `Convert_v1alpha1_*_To_v1alpha2_*` functions in `kubedb.dev/apimachinery`, then re-applies defaults from a catalog version.
- `check-deprecated` -- lists installed v1alpha1 KubeDB resources (cluster or local `--dir`) so users can find what needs converting.
- `compare` -- inventories managed databases on public clouds and DBaaS vendors and estimates the savings of migrating them to KubeDB. All logic is in the `pkg/compare` package; the command wiring is `pkg/cmds/compare.go`. See "compare architecture" below.

`LoadCatalog` in `calculate.go` is the shared catalog loader for `convert`: it seeds `*Version` objects from the embedded `kubedb.dev/installer/catalog/kubedb` FS, then layers on custom `*Version` CRs from the live cluster (unless `--local`). Defaulters require these catalog entries -- missing entries cause `convert` to fail with `"unknown %v version %s"`.

## Build / test / lint

All targets run inside the `ghcr.io/appscode/golang-dev:1.25` container via the Makefile -- do not invoke `go build` etc. directly unless you're matching that environment. Vendor mode is mandatory (`GOFLAGS=-mod=vendor` is set in `hack/build.sh`, `hack/test.sh`, and the lint target).

- `make build` -- produces `bin/resource-calculator-<os>-<arch>`. Use `make build-linux_amd64` etc. for cross targets; `make all-build` builds every platform in `BIN_PLATFORMS`.
- `make test` (alias of `unit-tests`) -- runs `go test ./pkg/...`.
- `make lint` -- runs `golangci-lint` (config in `.golangci.yml`: default linters + `unparam`, gofmt rewrites `interface{}` -> `any`, generated files and `client/`, `vendor/` excluded).
- `make ci` -- what GitHub Actions runs: `verify check-license lint build unit-tests`.
- `make verify` -- `verify-gen` + `verify-modules` (`go mod tidy && go mod vendor` must produce no diff).
- `make fmt` -- runs `reimport3.py`, `goimports`, `gofmt -s` (note: import grouping is enforced by `reimport3.py`, which only lives in the build image).
- `make add-license` / `make check-license` -- `ltag` with template in `hack/license/`. Every new Go file needs the Apache 2.0 header from that template.

Single-test run (inside the build container, or locally if you have the toolchain): `go test -mod=vendor ./pkg/compare/ -run TestName`.

See `TEST.md` for the full test process (including how to test `compare` offline with JSON fixtures) and `DEVELOPMENT.md` for local setup and conventions.

## compare architecture (pkg/compare)

KubeDB is licensed on a single metric: memory allocated to database servers, counted as `replicas x memory per replica`. `compare` reduces every managed database to that number, sums it across the estate, and prices it against current managed spend.

Pipeline (stages after discovery are provider agnostic, so adding or changing a provider only touches discovery and that provider's sizing/price):

```
flags -> Options + KubeDBPricing  ->  DiscovererFor(p).Discover  ->  []ManagedDatabase  ->  BuildReport  ->  Render (text/json/yaml)
```

- `compare.go` -- core types: `ManagedDatabase` (`TotalMemoryGiB = MemoryGiBPerNode * NodeCount`), `KubeDBPricing` (USD/GiB/month, prod vs non-prod, 100 GiB production floor), `Report`, and `BuildReport` (savings = current managed spend - KubeDB cost).
- `discover.go` -- the `Discoverer` interface, the `Source` enum (`sdk`, `cli`, `rest`, `file`, `auto`), `Options`, `DiscovererFor`, and shared helpers (`runCLIJSON`, `httpJSON`, the `collector` pattern, `discoverViaFile`). `auto` prefers the SDK (REST for ClickHouse).
- Per provider there are two files: `<provider>.go` holds the CLI/REST discoverer, the JSON parsers, the managed-price anchors, and calls into sizing; `<provider>_sdk.go` holds the official-SDK discoverer. Both reuse the same sizing and pricing helpers, so every source yields identical `ManagedDatabase` records.
- `catalog.go` -- sizing: instance class / SKU / tier -> `InstanceSpec{VCPU, MemoryGiB}`. AWS `db.*`/`cache.*`/`*.search` mirror the underlying EC2 family memory; GCP `db-custom-CPU-MEMMB` encodes memory in the name; Atlas M-tiers and others have explicit tables. Unknown types are reported as a warning and excluded from the total rather than guessed.
- `report.go` -- text (tabwriter, sorted by footprint), JSON, YAML.

Providers: `aws`, `azure`, `gcp`, `oci` (hyperscalers: SDK + CLI + file); `atlas`, `elastic` (DBaaS: SDK + REST + file); `clickhouse` (REST + file, no official Go SDK). The CLI/file paths share parsers via `collector`; a `--from-file` bundle is a JSON object keyed by collector (formats in `docs/compare.md`). The managed-cost numbers are memory-normalized list-price anchors flagged `CostEstimated`; the KubeDB rate is a required flag (the public rate is quote-based, so there is no default).

## Things to know before changing code

- `pkg/cmds/calculate.go` `Convert_kubedb_v1alpha1_To_v1alpha2` and the `registeredKubeDBTypes` list in `check_deprecated.go` must stay in sync -- adding a new KubeDB kind to one without the other will silently skip it.
- `TerminationPolicyPause` is a local constant (`"Pause"`) that doesn't exist in v1alpha2; conversion rewrites it to `DeletionPolicyHalt`. Preserve that mapping when touching conversion code.
- `replace sigs.k8s.io/controller-runtime => github.com/kmodules/controller-runtime ...` in `go.mod` is intentional -- don't drop it when running `go mod tidy`.
- After any dependency or generated-file change, run `make verify` locally; CI fails on a non-clean `git diff` after `go mod tidy && go mod vendor`.
- Version metadata (`Version`, `GitTag`, etc. in `version.go`) is injected via `-ldflags` from `hack/build.sh`; leave the package-level `var` block alone.

Specific to `compare` and its cloud SDKs:

- Adding a provider: implement `Discoverer`, register it in `DiscovererFor` and `AllProviders`, add sizing to `catalog.go` and a price anchor. Reuse `collector` + `discoverViaFile` for a CLI/JSON provider or `httpJSON` for a REST one, and add a `<provider>_sdk.go` for the SDK source.
- `google.golang.org/api` is pinned (currently `v0.270.0`) to a release whose `go` directive is `<=` the repo `go` directive (`go 1.25.5`). Newer releases require a newer toolchain and would force a `toolchain` line into `go.mod`. Keep it pinned when bumping deps.
- `go get` can silently downgrade the `kubedb.dev/*` and `kubestash.dev/*` pseudo-version pins (they share the transitive `github.com/Azure/azure-sdk-for-go` umbrella graph). After any `go get`, re-pin them to the versions in `go.mod` and confirm `git diff go.mod` shows only intended changes.
- ClickHouse Cloud has no official Go control-plane SDK; it stays on REST (`--source=sdk` is rejected for it).
- `errSDKNotBuiltIn` is only used by ClickHouse now; the other six providers have real SDK discoverers.

## Related docs

- `README.md` -- user guide.
- `DESIGN.md` -- architecture and design decisions.
- `TEST.md` -- test process.
- `DEVELOPMENT.md` -- local development setup and conventions.
- `docs/compare.md` -- full `compare` reference (sources, credentials, file-bundle formats, node counting).
