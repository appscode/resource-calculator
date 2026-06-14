# AGENTS.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository. `CLAUDE.md` includes this file via `@AGENTS.md`.

## What this is

`resource-calculator` is a Cobra-based CLI (single binary, `main.go` -> `pkg/cmds`) that is shipped both as a standalone binary and as a `kubectl` plugin.

Subcommands live under `pkg/cmds/`:

- `calculate` -- walks every GVK registered in `kmodules.xyz/resource-metrics` `api.RegisteredTypes()`, lists objects via the dynamic client, sums CPU/memory/storage via `resourcemetrics.AppResourceLimits`, and prints a per-kind table or JSON/YAML. Picks the highest available API version per `GroupKind` using `kmodules.xyz/apiversion`. Supports `--all` to iterate every kubeconfig context.
- `convert` -- converts KubeDB `kubedb/v1alpha1` resources (Elasticsearch, Etcd, MariaDB, Memcached, MongoDB, MySQL, PerconaXtraDB, Postgres, Redis) to `v1alpha2` using the generated `Convert_v1alpha1_*_To_v1alpha2_*` functions in `kubedb.dev/apimachinery`, then re-applies defaults from a catalog version.
- `check-deprecated` -- lists installed v1alpha1 KubeDB resources (cluster or local `--dir`) so users can find what needs converting.
- `inspect` -- inventories managed databases on public clouds and DBaaS vendors and lists each one with its allocated CPU and memory plus the estate totals. All logic is in the `pkg/compare` package (the Go package name is unchanged; only the CLI command is `inspect`, exported as `NewCmdInspect`); the command wiring is `pkg/cmds/compare.go`. See "inspect architecture" below.

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

See `TEST.md` for the full test process (including how to test `inspect` offline with JSON fixtures) and `DEVELOPMENT.md` for local setup and conventions.

## inspect architecture (pkg/compare)

`inspect` reduces every managed database to its allocated CPU and memory per node times node count (counted as `replicas x size per replica`), sums it across the estate, and reports the totals. The Go package is still named `pkg/compare`; only the CLI command was renamed to `inspect` (exported entrypoint `NewCmdInspect`).

Pipeline (stages after discovery are provider agnostic, so adding or changing a provider only touches discovery and that provider's sizing/cost anchor):

```
flags -> Options  ->  DiscovererFor(p).Discover  ->  []ManagedDatabase  ->  BuildReport  ->  Render (text/json/yaml)
```

- `compare.go` -- core types: `ManagedDatabase` (`TotalMemoryGiB = MemoryGiBPerNode * NodeCount`, with the matching vCPU total `VCPUPerNode * NodeCount`), `Report`, and `BuildReport(scope, dbs, warnings)`, which sorts the databases and sums the estate totals (database count, node count, total vCPU, total memory).
- `discover.go` -- the `Discoverer` interface, the `Source` enum (`sdk`, `cli`, `rest`, `file`, `auto`), `Options`, `DiscovererFor`, and shared helpers (`runCLIJSON`, `httpJSON`, the `collector` pattern, `discoverViaFile`). `auto` prefers the SDK (REST for ClickHouse).
- Per provider there are two files: `<provider>.go` holds the CLI/REST discoverer, the JSON parsers, the managed-cost anchors, and calls into sizing; `<provider>_sdk.go` holds the official-SDK discoverer. Both reuse the same sizing and cost-estimate helpers, so every source yields identical `ManagedDatabase` records.
- `catalog.go` -- sizing: instance class / SKU / tier -> `InstanceSpec{VCPU, MemoryGiB}`. AWS `db.*`/`cache.*`/`*.search` mirror the underlying EC2 family memory; GCP `db-custom-CPU-MEMMB` encodes memory in the name; Atlas M-tiers and others have explicit tables. Unknown types are reported as a warning and excluded from the totals rather than guessed.
- `report.go` -- text (tabwriter, sorted by footprint, ending with the database/node counts and total vCPU/memory), JSON, YAML.

Providers: `aws`, `azure`, `gcp`, `oci` (hyperscalers: SDK + CLI + file); `atlas`, `elastic` (DBaaS: SDK + REST + file); `clickhouse` (REST + file, no official Go SDK). The CLI/file paths share parsers via `collector`; a `--from-file` bundle is a JSON object keyed by collector (formats in `docs/inspect.md`). For cloud providers each `ManagedDatabase` also carries an informational estimated managed monthly cost (memory-normalized list-price anchor, flagged `CostEstimated`), rendered as an EST. $/MO column and not part of the CPU/memory totals.

`inspect operators` is a separate, cluster-scoped path (not a cloud `Source`). It detects alternative database operators (CloudNativePG, Zalando, Percona, Strimzi, ECK, Altinity, cass-operator, and more) by CRD group/version/kind using the controller-runtime client with unstructured objects, reads CPU/memory-limit (falling back to requests) x replicas from each CR, and reports the allocated CPU and memory of the self-hosted estate (`Report.SelfHosted`, a per-operator/vendor breakdown). The operator catalog and per-CR extractors are in `operators.go`; the scan (`DiscoverOperators`) is in `kubernetes.go`. It also detects databases shipped as Bitnami/Chainguard/Docker Hardened Images by inspecting StatefulSet/Deployment container images (`DiscoverImageWorkloads` in `kubernetes.go`, image catalog in `images.go`). No project is added to `go.mod` for any of this; detection and reading are entirely through unstructured objects (controller-runtime is already a dependency).

## Things to know before changing code

- `pkg/cmds/calculate.go` `Convert_kubedb_v1alpha1_To_v1alpha2` and the `registeredKubeDBTypes` list in `check_deprecated.go` must stay in sync -- adding a new KubeDB kind to one without the other will silently skip it.
- `TerminationPolicyPause` is a local constant (`"Pause"`) that doesn't exist in v1alpha2; conversion rewrites it to `DeletionPolicyHalt`. Preserve that mapping when touching conversion code.
- `replace sigs.k8s.io/controller-runtime => github.com/kmodules/controller-runtime ...` in `go.mod` is intentional -- don't drop it when running `go mod tidy`.
- After any dependency or generated-file change, run `make verify` locally; CI fails on a non-clean `git diff` after `go mod tidy && go mod vendor`.
- Version metadata (`Version`, `GitTag`, etc. in `version.go`) is injected via `-ldflags` from `hack/build.sh`; leave the package-level `var` block alone.

Specific to `inspect` and its cloud SDKs:

- Adding a provider: implement `Discoverer`, register it in `DiscovererFor` and `AllProviders`, add sizing to `catalog.go` and a price anchor. Reuse `collector` + `discoverViaFile` for a CLI/JSON provider or `httpJSON` for a REST one, and add a `<provider>_sdk.go` for the SDK source.
- `google.golang.org/api` is pinned (currently `v0.270.0`) to a release whose `go` directive is `<=` the repo `go` directive (`go 1.25.5`). Newer releases require a newer toolchain and would force a `toolchain` line into `go.mod`. Keep it pinned when bumping deps.
- `go get` can silently downgrade the `kubedb.dev/*` and `kubestash.dev/*` pseudo-version pins (they share the transitive `github.com/Azure/azure-sdk-for-go` umbrella graph). After any `go get`, re-pin them to the versions in `go.mod` and confirm `git diff go.mod` shows only intended changes.
- ClickHouse Cloud has no official Go control-plane SDK; it stays on REST (`--source=sdk` is rejected for it).
- `errSDKNotBuiltIn` is only used by ClickHouse now; the other six providers have real SDK discoverers.
- Adding an operator to `inspect operators`: add a descriptor (GVK + an unstructured extractor for replicas and memory) to `operators.go`. Do NOT add a go.mod dependency on the operator's project; detect and read via unstructured objects only.

## Related docs

- `README.md` -- user guide.
- `DESIGN.md` -- architecture and design decisions.
- `TEST.md` -- test process.
- `DEVELOPMENT.md` -- local development setup and conventions.
- `docs/inspect.md` -- full `inspect` reference (sources, credentials, file-bundle formats, node counting).
