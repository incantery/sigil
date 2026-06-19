# Sigil dev observability: the LGTM stack for scenario traces.
#
# `sigil test --otlp http://localhost:4318` (and `make gauntlet-suite`)
# push scenario traces here; review them at http://localhost:3000.
#
# Why Tilt + k8s instead of docker compose: with a remote docker host
# (DOCKER_HOST=ssh://…) compose publishes ports on the *remote* box, so
# localhost:4318/:3000 never reach you. Tilt's port_forwards go through the
# k8s API, so localhost works regardless of where the cluster runs.
#
#   tilt up        # bring LGTM up, wait for the `lgtm` resource to go green
#   make gauntlet-suite
#   tilt down

# Dev clusters only — never deploy LGTM to a prod context. Mirrors dora.
allow_k8s_contexts(['docker-desktop', 'minikube', 'kind-kind', 'orbstack', 'homelab'])

k8s_yaml('local-k8s/lgtm.yaml')

k8s_resource(
    'lgtm',
    port_forwards=[
        '3000:3000',  # Grafana
        '4318:4318',  # OTLP/HTTP  (what `sigil test --otlp` posts to)
        '4317:4317',  # OTLP/gRPC
        '3200:3200',  # Tempo query API
    ],
    labels=['observability'],
)
