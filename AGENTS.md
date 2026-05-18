# AGENTS.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`resource-calculator` is a Cobra-based CLI (single binary, `main.go` → `pkg/cmds`) that operates against a Kubernetes cluster via kubeconfig. It is shipped as both a standalone binary and a `kubectl` plugin.

Three subcommands live under `pkg/cmds/`:

- `calculate` — walks every GVK registered in `kmodules.xyz/resource-metrics` `api.RegisteredTypes()`, lists objects via the dynamic client, sums CPU/memory/storage via `resourcemetrics.AppResourceLimits`, and prints a per-kind table or JSON/YAML. Picks the highest available API version per `GroupKind` using `kmodules.xyz/apiversion`. Supports `--all` to iterate every kubeconfig context.
- `convert` — converts KubeDB `kubedb/v1alpha1` resources (Elasticsearch, Etcd, MariaDB, Memcached, MongoDB, MySQL, PerconaXtraDB, Postgres, Redis) to `v1alpha2` using the generated `Convert_v1alpha1_*_To_v1alpha2_*` functions in `kubedb.dev/apimachinery`, then re-applies defaults from a catalog version.
- `check-deprecated` — lists installed v1alpha1 KubeDB resources (cluster or local `--dir`) so users can find what needs converting.

`LoadCatalog` in `calculate.go` is the shared catalog loader for `convert`: it seeds `*Version` objects from the embedded `kubedb.dev/installer/catalog/kubedb` FS, then layers on custom `*Version` CRs from the live cluster (unless `--local`). Defaulters require these catalog entries — missing entries cause `convert` to fail with `"unknown %v version %s"`.

## Build / test / lint

All targets run inside the `ghcr.io/appscode/golang-dev:1.25` container via the Makefile — do not invoke `go build` etc. directly unless you're matching that environment. Vendor mode is mandatory (`GOFLAGS=-mod=vendor` is set in `hack/build.sh`, `hack/test.sh`, and the lint target).

- `make build` — produces `bin/resource-calculator-<os>-<arch>`. Use `make build-linux_amd64` etc. for cross targets; `make all-build` builds every platform in `BIN_PLATFORMS`.
- `make test` (alias of `unit-tests`) — runs `go test ./pkg/...`.
- `make lint` — runs `golangci-lint` (config in `.golangci.yml`: default linters + `unparam`, gofmt rewrites `interface{}` → `any`, generated files and `client/`, `vendor/` excluded).
- `make ci` — what GitHub Actions runs: `verify check-license lint build unit-tests`.
- `make verify` — `verify-gen` + `verify-modules` (`go mod tidy && go mod vendor` must produce no diff).
- `make fmt` — runs `reimport3.py`, `goimports`, `gofmt -s` (note: import grouping is enforced by `reimport3.py`, which only lives in the build image).
- `make add-license` / `make check-license` — `ltag` with template in `hack/license/`. Every new Go file needs the Apache 2.0 header from that template.

Single-test run (inside the build container, or locally if you have the toolchain): `go test -mod=vendor ./pkg/cmds/ -run TestName`.

## Things to know before changing code

- `pkg/cmds/calculate.go` `Convert_kubedb_v1alpha1_To_v1alpha2` and the `registeredKubeDBTypes` list in `check_deprecated.go` must stay in sync — adding a new KubeDB kind to one without the other will silently skip it.
- `TerminationPolicyPause` is a local constant (`"Pause"`) that doesn't exist in v1alpha2; conversion rewrites it to `DeletionPolicyHalt`. Preserve that mapping when touching conversion code.
- `replace sigs.k8s.io/controller-runtime => github.com/kmodules/controller-runtime ...` in `go.mod` is intentional — don't drop it when running `go mod tidy`.
- After any dependency or generated-file change, run `make verify` locally; CI fails on a non-clean `git diff` after `go mod tidy && go mod vendor`.
- Version metadata (`Version`, `GitTag`, etc. in `version.go`) is injected via `-ldflags` from `hack/build.sh`; leave the package-level `var` block alone.
