# Development

Things worth knowing before working on `resource-calculator`. For the test
process see `TEST.md`; for architecture see `DESIGN.md`; for a concise
agent-oriented summary see `AGENTS.md`.

## Prerequisites

- Docker, for the pinned build image `ghcr.io/appscode/golang-dev:1.25`. All
  Makefile targets run inside it, so your toolchain always matches CI.
- Optional: a local Go toolchain matching the `go` directive in `go.mod`
  (`go 1.25.5`) if you want to run `go test`/`go build` directly. Always use
  `-mod=vendor`.

## Layout

```
main.go            entrypoint -> pkg/cmds.NewRootCmd
pkg/cmds/          one file per subcommand (calculate, convert, check-deprecated, compare) + root
pkg/compare/       the compare feature (provider-agnostic engine + per-provider discoverers)
hack/              build.sh, test.sh, fmt.sh, license templates
docs/compare.md    compare reference
vendor/            vendored dependencies (mandatory; kept in sync by make verify)
```

## Build and dev loop

```bash
make build     # bin/resource-calculator-<os>-<arch>
make fmt       # reimport3.py + goimports + gofmt -s (import grouping lives in the build image)
make verify    # go mod tidy && go mod vendor must produce no diff
make ci        # full gate: verify check-license lint build unit-tests
```

Run `make fmt` (not a bare `gofmt`) before committing if you touched imports,
because import grouping is enforced by `reimport3.py`, which only exists in the
build image.

## Conventions

- License header: every new Go file needs the Apache 2.0 header from
  `hack/license/go.txt`. Run `make add-license` to apply, `make check-license`
  to verify.
- `gofmt` rewrites `interface{}` to `any` (configured in `.golangci.yml`); write
  `any`.
- Vendor mode is mandatory. Do not commit a change that makes
  `go mod tidy && go mod vendor` produce a diff (`make verify` enforces this).
- `golangci-lint` runs `unparam`. Avoid always-nil return values and unused
  parameters (drop an unused `ctx`/`opts` rather than keeping it for symmetry).

## Dependencies and vendoring

- The `replace sigs.k8s.io/controller-runtime => github.com/kmodules/controller-runtime ...`
  line in `go.mod` is intentional. Keep it.
- `google.golang.org/api` is pinned (currently `v0.270.0`) to a release whose
  `go` directive is `<=` the repo `go` directive. Newer releases require Go
  `>= 1.25.8` and would inject a `toolchain` line into `go.mod`. When bumping
  dependencies, keep this pin (or bump the repo `go` directive deliberately).
- `go get` can silently downgrade the `kubedb.dev/*` and `kubestash.dev/*`
  pseudo-version pins, because they share the transitive
  `github.com/Azure/azure-sdk-for-go` umbrella graph. After any `go get`,
  re-pin them to the versions already in `go.mod` and check `git diff go.mod`
  shows only intended changes before running `make verify`.
- `github.com/Azure/azure-sdk-for-go/sdk/azidentity` may resolve to a beta
  (driven by the same graph coupling). Pin it to a stable release only if you
  can do so without disturbing the kubedb pins.

## Working on `convert` / `check-deprecated`

`Convert_kubedb_v1alpha1_To_v1alpha2` in `pkg/cmds/calculate.go` and the
`registeredKubeDBTypes` list in `pkg/cmds/check_deprecated.go` must stay in
sync. Adding a KubeDB kind to one but not the other silently skips it.
Conversion rewrites the local `TerminationPolicyPause` ("Pause") constant to
`DeletionPolicyHalt`; preserve that mapping.

## Working on `compare`

The engine (`compare.go`), sources (`discover.go`), sizing (`catalog.go`),
pricing/savings, and rendering (`report.go`) are provider agnostic. A provider
only owns discovery plus its sizing and price anchor.

To add a provider:

1. Implement `Discoverer` in `pkg/compare/<provider>.go` (CLI/REST + file
   parsers). Reuse the `collector` pattern with `discoverViaFile` for a
   CLI/JSON provider, or `httpJSON` for a REST one.
2. Add sizing to `catalog.go` (instance class / SKU / tier to
   `InstanceSpec{VCPU, MemoryGiB}`) and a managed-price anchor in the provider
   file. Unknown types should warn and be excluded, not guessed.
3. Register the provider in `DiscovererFor` and `AllProviders`.
4. Optionally add `pkg/compare/<provider>_sdk.go` with a `discoverViaSDK`
   method and dispatch to it from the provider's `SourceSDK`/`SourceAuto`
   cases. Reuse the same sizing/pricing helpers so all sources match.
5. Add a parser test and a catalog test (see `TEST.md`).

The `compare operators` command (in-cluster scan) is separate from the cloud
providers. It detects databases two ways, both through the controller-runtime
client and unstructured objects, with no dependency on the scanned projects:

- Operator-managed CRs: add a descriptor (GVK plus an extractor that reads
  replicas and memory from the CR) to `operators.go`. The scan is
  `DiscoverOperators` in `kubernetes.go`.
- Vendor images (Bitnami / Chainguard / Docker Hardened Images): add the image
  base name to `dbImageNames` in `images.go` (and, for a new vendor, the
  registry match in `classifyDBImage` plus an `imageVendorLicensing` entry). The
  scan is `DiscoverImageWorkloads` in `kubernetes.go`.

Do not add a go.mod dependency on any operator or image project; detect and read
everything through unstructured objects. Add an extractor or classifier test
(see `TEST.md`).

Notes:

- ClickHouse Cloud has no official Go control-plane SDK; it stays on REST and
  rejects an explicit `--source=sdk` (`errSDKNotBuiltIn`).
- The KubeDB rate is a required flag (`--kubedb-rate-prod` /
  `--kubedb-rate-nonprod`); the public rate is quote-based, so there is no
  default. Managed-cost numbers are memory-normalized list-price estimates,
  flagged `CostEstimated`.
- When changing a discoverer, verify both the live path and the file path
  (they share parsers, so a fixture test covers most of it).
