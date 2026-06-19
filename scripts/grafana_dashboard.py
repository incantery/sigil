#!/usr/bin/env python3
"""Install/update the Sigil scenarios dashboard in Grafana, print its URL.

otel-lgtm provisions its own datasources, so we can't hardcode the Tempo
UID. This queries Grafana for the live UID, injects it into
docs/observability/sigil-dashboard.json, and POSTs the dashboard
(overwrite). On success prints the dashboard URL; on any failure prints a
usable fallback URL so the suite output is always actionable.

Usage: grafana_dashboard.py [grafana_base_url]   (default http://localhost:3000)
"""
import json
import os
import sys
import urllib.error
import urllib.request

GRAFANA = (sys.argv[1] if len(sys.argv) > 1 else "http://localhost:3000").rstrip("/")
HERE = os.path.dirname(os.path.abspath(__file__))
DASH_PATH = os.path.normpath(os.path.join(HERE, "..", "docs", "observability", "sigil-dashboard.json"))


def get_json(url, timeout=3):
    with urllib.request.urlopen(url, timeout=timeout) as r:
        return json.load(r)


def ds_uids():
    """Map datasource type -> uid for the types the dashboard references."""
    out = {}
    try:
        for ds in get_json(GRAFANA + "/api/datasources"):
            out.setdefault(ds.get("type"), ds.get("uid"))
    except Exception:
        return out
    return out


def main():
    uids = ds_uids()
    if "tempo" not in uids:
        print(f"{GRAFANA}   (Grafana not reachable — is the 'lgtm' resource green in Tilt?)")
        return
    raw = open(DASH_PATH).read().replace("__TEMPO_UID__", uids["tempo"])
    if "prometheus" in uids:
        raw = raw.replace("__PROM_UID__", uids["prometheus"])
    dash = json.loads(raw)
    payload = json.dumps({"dashboard": dash, "overwrite": True}).encode()
    req = urllib.request.Request(
        GRAFANA + "/api/dashboards/db",
        data=payload,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        res = json.load(urllib.request.urlopen(req, timeout=5))
        print(GRAFANA + res.get("url", "/d/sigil-scenarios/sigil-scenario-runs"))
    except urllib.error.HTTPError as e:
        # e.g. anonymous role can't write dashboards — fall back to the URL.
        print(f"{GRAFANA}/d/sigil-scenarios   (could not auto-install dashboard: HTTP {e.code})")
    except Exception as e:
        print(f"{GRAFANA}/d/sigil-scenarios   (dashboard install failed: {e})")


if __name__ == "__main__":
    main()
