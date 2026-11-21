#!/usr/bin/env bash
# Chart render assertions. Run: bash charts/amr-fleet-control/tests/render_test.sh (needs helm and python3 + PyYAML)
set -euo pipefail
chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_dir="$(cd "$chart_dir/../.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }
vals=(--set database.url=postgres://x --set kafka.brokers=k:9092)
render() { helm template amr-fleet-control "$chart_dir" "${vals[@]}" "$@"; }

deployment="$(render --show-only templates/deployment.yaml)"
configmap="$(render --show-only templates/configmap.yaml)"
cm_name="$(awk '/^  name:/ {print $2; exit}' <<<"$configmap")"
grep -A3 'envFrom:' <<<"$deployment" | grep -q "name: $cm_name" || fail "the api Deployment does not mount the ConfigMap via envFrom"

# Every variable cmd/api reads is in the ConfigMap or the Secret env, and nothing else (Task 16).
read_vars="$(grep -ohE '(getenv|r\.or|r\.atLeast|r\.seconds|r\.positive)\("[A-Z_]+"' "$repo_dir"/cmd/api/config.go "$repo_dir"/cmd/api/telemetry.go \
  | grep -oE '"[A-Z_]+"' | tr -d '"' | sort -u)"
read_vars="$(printf '%s\nMIGRATIONS_DATABASE_URL\n' "$read_vars" | sort -u)"
[ "$(wc -l <<<"$read_vars")" -ge 25 ] || fail "found only $(wc -l <<<"$read_vars") env names in cmd/api: extend the pattern, never lower the floor"
rendered="$( (grep -oE '^  [A-Z_]+:' <<<"$configmap" | tr -d ' :'; grep -oE 'name: [A-Z_]+$' <<<"$deployment" | awk '{print $2}') | sort -u)"
diff <(echo "$read_vars") <(echo "$rendered") || fail "the api env differs from what cmd/api reads (left: read, right: rendered)"

grep -q '^  map.yaml: ' <<<"$configmap" || fail "ConfigMap does not carry map.yaml"
cmp -s "$chart_dir/files/map.yaml" "$repo_dir/config/map.example.yaml" || fail "files/map.yaml differs from config/map.example.yaml"
# deviation (Task 21): the brief fed the ConfigMap on stdin AND the script as a heredoc; the heredoc wins, so the
# script read an empty stdin. The ConfigMap goes through a file instead (material-flow-control's render_test shape).
cm_file="$(mktemp)"; trap 'rm -f "$cm_file"' EXIT
printf '%s\n' "$configmap" > "$cm_file"
python3 - "$repo_dir/config/map.example.yaml" "$cm_file" <<'PY' || fail "the default map.yaml is not config/map.example.yaml"
import sys, yaml
cm = yaml.safe_load(open(sys.argv[2]))
assert yaml.safe_load(cm["data"]["map.yaml"]) == yaml.safe_load(open(sys.argv[1]))
PY
grep -q 'DATABASE_URL' <<<"$configmap" && fail "the ConfigMap must not carry the DSN"
grep -A4 'name: DATABASE_URL$' <<<"$deployment" | grep -q secretKeyRef || fail "DATABASE_URL must come from a Secret"
grep -q 'replicas: 1$' <<<"$deployment" || fail "the api must run exactly one replica"
grep -q 'type: Recreate' <<<"$deployment" || fail "the rollout must be Recreate (never two traffic controllers)"
grep -q 'readOnlyRootFilesystem: true' <<<"$deployment" || fail "root filesystem must be read-only"
grep -q 'runAsNonRoot: true' <<<"$deployment" || fail "the pod must run as non-root"
# carry (plan 04 Task 23 review lesson d): the api's stop sequence (10 s HTTP drain overlapping the loops' stop, a relay
# pass, the MQTT disconnect) is bounded at about 20 s; the grace period defaults to 45 and the chart refuses less than 30.
grace="$(grep -oE 'terminationGracePeriodSeconds: [0-9]+' <<<"$deployment" | awk '{print $2}')"
[ "${grace:-0}" -ge 45 ] || fail "api terminationGracePeriodSeconds is ${grace:-unset}, want >= 45 (30 + margin)"
render --set terminationGracePeriodSeconds=29 >/dev/null 2>&1 && fail "terminationGracePeriodSeconds=29 rendered"
render --set terminationGracePeriodSeconds=30 >/dev/null 2>&1 || fail "terminationGracePeriodSeconds=30 refused"
grep -q 'path: /readyz' <<<"$deployment" || fail "readiness probe must hit /readyz"
# carry (plan 04 Task 23 review lesson d): startup and liveness on /healthz, readiness on /readyz, the map read-only.
dep_file="$(mktemp)"; trap 'rm -f "$cm_file" "$dep_file"' EXIT
printf '%s\n' "$deployment" > "$dep_file"
python3 - "$dep_file" <<'PY' || fail "api probes / mounts"
import sys, yaml
c = yaml.safe_load(open(sys.argv[1]))["spec"]["template"]["spec"]["containers"][0]
got = {p: c.get(p, {}).get("httpGet", {}).get("path") for p in ("startupProbe", "livenessProbe", "readinessProbe")}
assert got == {"startupProbe": "/healthz", "livenessProbe": "/healthz", "readinessProbe": "/readyz"}, got
mounts = [(m["name"], m["mountPath"], m.get("readOnly")) for m in c["volumeMounts"]]
assert mounts == [("map", "/etc/amr-fleet-control", True)], mounts
PY
grep -q 'mountPath: /etc/amr-fleet-control' <<<"$deployment" || fail "map.yaml is not mounted at the map file's directory"
grep -qE '_MODE|ENVIRONMENT' <<<"$deployment$configmap" && fail "no *_MODE or ENVIRONMENT variable may exist"
grep -q 'OTEL_EXPORTER_OTLP_ENDPOINT: "otel-collector' <<<"$configmap" || fail "otel must default on with the collector endpoint"
grep -q 'OTEL_' <<<"$(render --show-only templates/configmap.yaml --set otel.enabled=false)" && fail "otel.enabled=false must render no OTEL_* variable"

# Optional components are off by default and carry exactly their binary's env when on.
[ "$(render | grep -c '^kind: Deployment')" = 1 ] || fail "the default render must be the api Deployment only"
all=(-f "$chart_dir/ci/all-components-values.yaml")
mcp="$(helm template amr-fleet-control "$chart_dir" "${all[@]}" --show-only templates/mcp-deployment.yaml)"
a2a="$(helm template amr-fleet-control "$chart_dir" "${all[@]}" --show-only templates/a2a-deployment.yaml)"
# deviation (Task 21): [A-Z0-9_], the brief's [A-Z_] cannot match A2A_ADDR / A2A_AGENT_URL.
envs() { grep -oE '^            - name: [A-Z0-9_]+$' | awk '{print $3}' | sort -u | tr '\n' ' '; }
[ "$(envs <<<"$mcp")" = "AMR_MAP_FILE DATABASE_URL LOG_LEVEL MCP_ADDR MIGRATIONS_DATABASE_URL OTEL_EXPORTER_OTLP_ENDPOINT OTEL_SERVICE_NAME SERVICE_VERSION " ] \
  || fail "mcp env = $(envs <<<"$mcp")"
[ "$(envs <<<"$a2a")" = "A2A_ADDR A2A_AGENT_URL AMR_MAP_FILE DATABASE_URL LOG_LEVEL MIGRATIONS_DATABASE_URL OTEL_EXPORTER_OTLP_ENDPOINT OTEL_SERVICE_NAME SERVICE_VERSION " ] \
  || fail "a2a env = $(envs <<<"$a2a")"
grep -q 'envFrom' <<<"$mcp$a2a" && fail "mcp and a2a must not mount the api env (no Kafka or MQTT variable)"
grep -q 'value: "amr-fleet-control-mcp"' <<<"$mcp" || fail "the mcp service.name must be amr-fleet-control-mcp"
grep -q 'value: "http://amr-fleet-control-a2a.default.svc.cluster.local:8093/"' <<<"$a2a" || fail "A2A_AGENT_URL must default to the in-cluster Service"

# Refusals.
helm template amr-fleet-control "$chart_dir" --set kafka.brokers=k:9092 >/dev/null 2>&1 && fail "renders without a database"
helm template amr-fleet-control "$chart_dir" --set database.url=postgres://x >/dev/null 2>&1 && fail "renders without Kafka"
render --set mqtt.url= >/dev/null 2>&1 && fail "renders without MQTT"
render --set replicaCount=2 >/dev/null 2>&1 && fail "renders two api replicas"
# carry (plan 04 Task 23 review lesson d): never zero api replicas either.
render --set replicaCount=0 >/dev/null 2>&1 && fail "renders zero api replicas"
render --set terminationGracePeriodSeconds=5 >/dev/null 2>&1 && fail "renders a grace period below the shutdown bound"
render --set 'extraEnv[0].name=SCHEDULER_MODE' --set 'extraEnv[0].value=x' >/dev/null 2>&1 && fail "renders a *_MODE extraEnv"
other="$(render --show-only templates/configmap.yaml --set mqtt.url=tcp://x:1 --set config.chargeLowPercent=30)"
grep -q 'MQTT_URL: "tcp://x:1"' <<<"$other" || fail "mqtt.url does not reach MQTT_URL"
grep -q 'CHARGE_LOW_PERCENT: "30"' <<<"$other" || fail "config.chargeLowPercent does not reach its variable"
# carry (plan 04 Task 23 review lesson c): mcp and a2a read both database URLs from the chart's Secret (or the existing
# one), the migrations DSN included, and get no Kafka, MQTT or relay variable.
for secret in "" existing-db; do
  extra=(); [ -n "$secret" ] && extra=(--set database.existingSecret="$secret")
  for c in mcp a2a; do
    d_file="$(mktemp)"
    helm template amr-fleet-control "$chart_dir" "${all[@]}" --set database.url=postgres://x --set database.existingSecret= \
      ${extra[@]+"${extra[@]}"} --show-only "templates/$c-deployment.yaml" > "$d_file"
    python3 - "$d_file" "$c" "${secret:-amr-fleet-control-db}" <<'PY' || { rm -f "$d_file"; fail "$c database env (secret ${secret:-chart})"; }
import sys, yaml
path, comp, secret = sys.argv[1], sys.argv[2], sys.argv[3]
d = yaml.safe_load(open(path))
spec = d["spec"]["template"]["spec"]
c = spec["containers"][0]
env = {e["name"]: e for e in c["env"]}
for name in ("DATABASE_URL", "MIGRATIONS_DATABASE_URL"):
    ref = env[name].get("valueFrom", {}).get("secretKeyRef", {})
    assert ref.get("name") == secret and ref.get("key") == name, f"{comp} {name} is {env[name]}, want secretKeyRef {secret}/{name}"
    assert "value" not in env[name], f"{comp} {name} has a literal value"
bad = sorted(n for n in env if n.startswith(("KAFKA_", "MQTT_", "OUTBOX_")))
assert not bad, f"{comp} gets {bad}"
assert "envFrom" not in c, f"{comp} mounts an env ConfigMap"
assert [m["mountPath"] for m in c["volumeMounts"] if m.get("readOnly")] == ["/etc/amr-fleet-control"], f"{comp} mounts {c['volumeMounts']}"
for p in ("startupProbe", "livenessProbe", "readinessProbe"):
    assert c[p]["httpGet"]["path"] == "/healthz", f"{comp} {p} {c[p]}"
PY
    rm -f "$d_file"
  done
done
echo "render_test: PASS"
