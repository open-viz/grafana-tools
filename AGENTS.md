# AGENTS.md

This file provides guidance to coding agents (e.g. Claude Code, claude.ai/code) when working with code in this repository.

## Repository purpose

Go module `go.openviz.dev/grafana-tools` — the OpenViz Grafana operator. It runs as a Kubernetes aggregated API server *and* a set of controllers that:

- Reconcile OpenViz CRs (`GrafanaDashboard`, `GrafanaDatasource`) into actual Grafana objects via the Grafana HTTP API.
- Watch Prometheus-Operator resources (`Prometheus`, `Alertmanager`, `ServiceMonitor`) and a couple of `Namespace` flavors, then wire up backend registration, RBAC, chart presets, Rancher token refresh, and "default" `AppBinding`s for Grafana/Prometheus.

The produced binary is `grafana-tools`. There are two long-running subcommands (split in `pkg/cmds/`): one for the apiserver+webhook process and one for the dashboard/datasource controllers.

See `DESIGN.md` for the higher-level design (default Grafana AppBinding, DashboardLinks, double-opt-in policy) and `pkg/controllers/README.md` for the authoritative reconciler reference.

## Architecture

- `cmd/grafana-tools/main.go` — entry point; builds a Cobra root command and runs.
- `pkg/cmds/` — Cobra subcommands and option wiring:
  - `root.go`, `grafana.go`, `monitoring.go` — top-level commands.
  - `server/` — `RecommendedOptions` and start glue for the apiserver flavor.
- `pkg/apiserver/apiserver.go` — aggregated API server config + scheme. Registers, conditionally:
  - The CRD bootstrap reconciler from `kmodules.xyz/client-go/apiextensions`.
  - `ranchertoken.TokenRefresher` only when `ExtraConfig.RancherAuthSecret` is set.
  - Prometheus / Alertmanager / ServiceMonitor controllers via `apiextensions.RegisterSetup(...)` (gated on CRD presence).
- `pkg/controllers/` — reconciler implementations. Each subdirectory hosts a focused controller:
  - `openviz/grafanadashboard_controller.go`, `grafanadatasource_controller.go` — reconcile OpenViz CRs against Grafana via `pkg/grafana/`.
  - `prometheus/`, `alertmanager/`, `servicemonitor/` — monitoring stack wiring.
  - `namespace/` — `ClientOrgReconciler` for client-org labeled namespaces (RBAC, backend registration, dashboard copy/transform).
  - `clientorg/` — client-org primitives.
  - `ranchertoken/` — token refresher.
  - `config.go` — shared controller config wiring.
  - `README.md` — table of every reconciler with its watches, finalizers, and key outputs. Keep this updated when you add/change a controller.
- `pkg/registry/` — `rest.Storage` glue for aggregated API resources, plus `ui/` sub-registry for projected views.
- `pkg/grafana/` — Grafana HTTP client (`client.go`), config (`config.go`), dashboard JSON building (`builder.go`). Wraps `github.com/grafana-tools/sdk` and `go.openviz.dev/grafana-sdk`.
- `pkg/detector/` — readiness checks (`prometheus.go`, `alertmanager.go`) gating controllers via `d.Ready()`.
- `pkg/eventer/recorder.go` — shared Kubernetes event recorder.
- `pkg/rancherutil/` — Rancher API helpers used by the token refresher and AppBinding wiring.
- `test/e2e/` — Ginkgo/Gomega e2e suite (`e2e_suite_test.go`, `dashboard_test.go`, `options_test.go`) with JSON fixtures.
- `Dockerfile.in` (PROD, distroless), `Dockerfile.dbg` (debian), `Dockerfile.ubi` (Red Hat certified) — three image variants built/pushed per release.

### Reconciler patterns

(From `pkg/controllers/README.md` — internalize before changing a reconciler.)

- Gate work on detector readiness (`d.Ready()`).
- Use finalizers for external cleanup (e.g. `monitoring.appscode.com/prometheus`).
- Use `CreateOrPatch` for owned resources.
- Use marker annotations to avoid repeated backend registration.
- Add watches with predicates so dependency changes requeue the parent.

API types come from `go.openviz.dev/apimachinery`; do not redefine them here.

## Common commands

All build/test/lint targets run inside the `ghcr.io/appscode/golang-dev` Docker image — Docker must be running.

- `make ci` — CI pipeline: `check-license lint build unit-tests` (note: `verify` is commented out of CI; run `make verify` manually before opening a PR if you changed `apis/` or vendor).
- `make build` — build the binary for the host OS/ARCH into `bin/<os>_<arch>/grafana-tools`.
- `make all-build` — build for all `BIN_PLATFORMS` (linux amd64/arm/arm64).
- `make fmt` — gofmt + goimports.
- `make lint` — golangci-lint.
- `make unit-tests` — Go unit tests.
- `make e2e-tests` — Ginkgo e2e suite under `test/e2e/`.
- `make test` — runs both `unit-tests` and `e2e-tests`.
- `make verify` — `verify-gen verify-modules` (re-run `gen fmt`, ensure `go mod tidy && go mod vendor` is clean).
- `make container` — build PROD, DBG, and UBI images.
- `make push` — push all three image variants; `make docker-manifest` writes multi-arch manifests; `make release` does both.
- `make push-to-kind` / `make deploy-to-kind` — load image into Kind and Helm-install.
- `make install` / `make uninstall` / `make purge` — Helm-managed install lifecycle.
- `make run` — run the binary locally against the current kubeconfig.
- `make add-license` / `make check-license` — manage license headers.

Run a single Go test locally (requires a Go toolchain):

```
go test ./pkg/controllers/openviz/... -run TestName -v
```

Run a single Ginkgo spec:

```
go test ./test/e2e/... -v -ginkgo.focus="dashboard"
```

## Conventions

- Module path is `go.openviz.dev/grafana-tools` (vanity URL); imports must use that, not the GitHub URL (`open-viz/grafana-tools`).
- License: Apache-2.0; every Go file carries the standard AppsCode header (`make add-license`).
- Sign off commits (`git commit -s`); contributions follow the DCO (`DCO`, `CONTRIBUTING.md`).
- Vendor directory is checked in — `go mod tidy && go mod vendor` must leave the tree clean (enforced by `verify-modules`).
- API types come from `go.openviz.dev/apimachinery`; bump that dep and re-vendor instead of redefining types here.
- When adding a new reconciler: place it in its own `pkg/controllers/<name>/` directory, register it in `pkg/apiserver/apiserver.go` or `pkg/cmds/server/grafana.go` (whichever process should host it), and update `pkg/controllers/README.md`'s reconciler summary table.
- Three Dockerfiles, one binary — keep `Dockerfile.in`, `Dockerfile.dbg`, and `Dockerfile.ubi` in sync when changing build steps.
