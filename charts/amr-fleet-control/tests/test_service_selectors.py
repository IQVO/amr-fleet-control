#!/usr/bin/env python3
"""Every Service selects exactly the pods of one component (api, mcp, a2a), and every Deployment pins its component.

Run: python3 charts/amr-fleet-control/tests/test_service_selectors.py   (needs helm and PyYAML)
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

CHART = Path(__file__).resolve().parents[1]


def render(values: str) -> list[dict]:
    out = subprocess.run(["helm", "template", "amr-fleet-control", str(CHART), "-f", str(CHART / "ci" / values)],
                         capture_output=True, text=True, check=True).stdout
    try:
        import yaml  # type: ignore
    except ModuleNotFoundError:
        print("FAIL: PyYAML not available; a selector test must not pass vacuously", file=sys.stderr)
        raise SystemExit(1)
    return [d for d in yaml.safe_load_all(out) if d]


def check(values: str, want: set[str], failures: list[str]) -> None:
    docs = render(values)
    deployments = [d for d in docs if d["kind"] == "Deployment"]
    services = [d for d in docs if d["kind"] == "Service"]
    pods = {}
    for d in deployments:
        match = d["spec"]["selector"]["matchLabels"]
        labels = d["spec"]["template"]["metadata"]["labels"]
        comp = match.get("app.kubernetes.io/component")
        if not comp:
            failures.append(f"{values}: Deployment {d['metadata']['name']} selector lacks app.kubernetes.io/component")
        if any(labels.get(k) != v for k, v in match.items()):
            failures.append(f"{values}: Deployment {d['metadata']['name']} pods do not satisfy its own selector")
        pods[d["metadata"]["name"]] = labels
    if {p.get("app.kubernetes.io/component") for p in pods.values()} != want:
        failures.append(f"{values}: components {sorted(p.get('app.kubernetes.io/component') for p in pods.values())}, want {sorted(want)}")
    if len(services) != len(want):
        failures.append(f"{values}: {len(services)} Services, want {len(want)}")
    for s in services:
        sel = s["spec"]["selector"]
        hit = [n for n, labels in pods.items() if all(labels.get(k) == v for k, v in sel.items())]
        if len(hit) != 1:
            failures.append(f"{values}: Service {s['metadata']['name']} selects {hit}, want exactly one Deployment")


def main() -> int:
    failures: list[str] = []
    check("default-values.yaml", {"api"}, failures)
    check("all-components-values.yaml", {"api", "mcp", "a2a"}, failures)
    if failures:
        print("FAIL: amr-fleet-control Service selectors")
        for f in failures:
            print(f"  - {f}")
        return 1
    print("ok: every Service selects exactly one component's Deployment (api; api, mcp, a2a)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
