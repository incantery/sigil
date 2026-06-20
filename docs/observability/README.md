# Scenario observability — the OTel spine

A `sigil test` run is a **trace**: each scenario is a root span, each step
a child span, with status, timing, intent attributes, and `extract` values
recorded as span events. The same trace is the AI's queryable artifact and
the human's dashboard — one substrate, two consumers.

The spine is **OTel-native, vendor-neutral, and optional**: there is no
hard OpenTelemetry SDK dependency, and tracing is off unless you ask for
it. The output is the OTLP/JSON shape any OTLP/HTTP backend accepts.

## Emit a trace

```sh
# write the run to a file (the AI's queryable artifact)
sigil test gauntlet/cross-page.sigil --trace run.json

# push to any OTLP/HTTP collector (Tempo, Datadog Agent, the OTel
# Collector, …) — /v1/traces is appended if you pass a bare base URL
sigil test gauntlet/cross-page.sigil --otlp http://localhost:4318

# prod-lean: keep structure, timing, and status; drop detail attributes
sigil test … --otlp http://collector:4318 --trace-lean
```

Both sinks may be set at once. `--trace-lean` is the "verbose vs standard"
knob: the same instrument at two verbosities, so test traces and prod
synthetic-monitor traces share one vocabulary.

## The Grafana/LGTM stack (optional, via Tilt + k8s)

The dashboard runs as the `grafana/otel-lgtm` all-in-one image (Grafana +
Tempo + an OTLP receiver, datasources pre-provisioned), deployed to a dev
k8s context by **Tilt** — see `../../Tiltfile` and `../../local-k8s/lgtm.yaml`.

We use Tilt + k8s rather than `docker compose` on purpose: with a remote
docker host (`DOCKER_HOST=ssh://…`) compose publishes ports on the *remote*
box, so `localhost:4318` / `localhost:3000` never reach you. Tilt's
`port_forwards` go through the k8s API, so `localhost` works regardless of
where the cluster or docker host lives. (Mirrors how dora runs LGTM.)

```sh
# needs a dev k8s context: docker-desktop / orbstack / homelab / kind / minikube
make observability-up          # = tilt up; wait for the `lgtm` resource to go green
make gauntlet-suite            # runs the suite, pushes traces, prints a Grafana URL
make observability-down        # = tilt down
```

`gauntlet-suite` installs/updates a curated dashboard — **Sigil scenario
runs** (`/d/sigil-scenarios`) — and prints its URL. It combines RED metrics
(from otel-lgtm's span-metrics generator, in Prometheus) with the trace
tables (Tempo):

- **Stats**: scenarios run in range, errored scenarios (red when > 0),
  step latency p50 / p95.
- **Step latency percentiles** (p50/p95/p99) and **scenario throughput by
  status** (OK green, ERROR red) over time.
- **Recent runs** / **Failed runs** tables — each row a scenario; click to
  open the step span tree (durations, `bind` events from `extract`, the
  failing step's status message).

Installed via Grafana's API (`scripts/grafana_dashboard.py` injects the
live Tempo + Prometheus datasource UIDs, since otel-lgtm provisions its
own). Install standalone with `make grafana-dashboard`. Source:
`sigil-dashboard.json`. The metric panels need otel-lgtm's span-metrics
generator (on by default); the tables work with just traces.

> First boot pulls the otel-lgtm image and the pod takes ~20–30s to pass
> its readiness probe; Tilt's port-forward goes live once `lgtm` is green.

## Trace shape

| Scenario concept | OTLP |
|---|---|
| Scenario run | root span (`scenario: <name>`), one trace |
| Step | child span (`step N: <verb>`), parent = the scenario |
| Pass / fail | span status OK / ERROR (+ failure message) |
| Step intent | attributes `sigil.verb`, `sigil.line/col`, `sigil.<arg>` |
| `extract` capture | a `bind` span event (`name`, `value`) |
| App / target | root-span attributes `sigil.app`, `sigil.target` |

Service name is `sigil-test`. trace/span ids are random per run.
