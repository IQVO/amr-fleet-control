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
# ruling P3-R16 / plan 04 P4-R18 (lesson b): every binary also reads what the telemetry adapter reads itself (ENVIRONMENT).
telemetry_vars="$(grep -ohE 'os\.Getenv\("[A-Z_]+"' "$repo_dir"/internal/adapters/outbound/telemetry/telemetry.go | grep -oE '"[A-Z_]+"' | tr -d '"')"
grep -qx ENVIRONMENT <<<"$telemetry_vars" || fail "the telemetry adapter no longer reads ENVIRONMENT: update this test"
read_vars="$(grep -ohE '(getenv|r\.or|r\.atLeast|r\.seconds|r\.positive)\("[A-Z_]+"' "$repo_dir"/cmd/api/config.go "$repo_dir"/cmd/api/telemetry.go \
  | grep -oE '"[A-Z_]+"' | tr -d '"' | sort -u)"
read_vars="$(printf '%s\nMIGRATIONS_DATABASE_URL\n%s\n' "$read_vars" "$telemetry_vars" | sort -u)"
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
# ruling P3-R16 / P4-R18 (lesson b) SUPERSEDES the brief's "no ENVIRONMENT": it labels telemetry only.
grep -qE '_MODE' <<<"$deployment$configmap" && fail "no *_MODE variable may exist"
grep -q 'OTEL_EXPORTER_OTLP_ENDPOINT: "otel-collector' <<<"$configmap" || fail "otel must default on with the collector endpoint"
grep -q 'OTEL_' <<<"$(render --show-only templates/configmap.yaml --set otel.enabled=false)" && fail "otel.enabled=false must render no OTEL_* variable"

# Optional components are off by default and carry exactly their binary's env when on.
[ "$(render | grep -c '^kind: Deployment')" = 1 ] || fail "the default render must be the api Deployment only"
all=(-f "$chart_dir/ci/all-components-values.yaml")
mcp="$(helm template amr-fleet-control "$chart_dir" "${all[@]}" --show-only templates/mcp-deployment.yaml)"
a2a="$(helm template amr-fleet-control "$chart_dir" "${all[@]}" --show-only templates/a2a-deployment.yaml)"
# deviation (Task 21): [A-Z0-9_], the brief's [A-Z_] cannot match A2A_ADDR / A2A_AGENT_URL.
envs() { grep -oE '^            - name: [A-Z0-9_]+$' | awk '{print $3}' | sort -u | tr '\n' ' '; }
[ "$(envs <<<"$mcp")" = "AMR_MAP_FILE DATABASE_URL ENVIRONMENT LOG_LEVEL MCP_ADDR MIGRATIONS_DATABASE_URL OTEL_EXPORTER_OTLP_ENDPOINT OTEL_SERVICE_NAME SERVICE_VERSION " ] \
  || fail "mcp env = $(envs <<<"$mcp")"
[ "$(envs <<<"$a2a")" = "A2A_ADDR A2A_AGENT_URL AMR_MAP_FILE DATABASE_URL ENVIRONMENT LOG_LEVEL MIGRATIONS_DATABASE_URL OTEL_EXPORTER_OTLP_ENDPOINT OTEL_SERVICE_NAME SERVICE_VERSION " ] \
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
# carry (plan 04 Task 23 review lesson a): the chart reads the PLATFORM keys warehouse-infra supplies
# (helm-values/_platform.yaml: kongPlugins.cors.origin, kongPlugins.opentelemetry.tracesEndpoint, mcp/a2a.route.parentRefs),
# like the sibling charts. Rendered as ArgoCD layers it (platform file first, then the app file), the plugins carry
# exactly the platform's origin and traces endpoint and every route attaches to the platform's Gateway.
fx="$chart_dir/tests/fixtures"
p_file="$(mktemp)"
helm template amr-fleet-control "$chart_dir" -f "$fx/platform-values.yaml" -f "$fx/app-values.yaml" > "$p_file" \
  || { rm -f "$p_file"; fail "the platform-shaped values do not render"; }
python3 - "$fx/platform-values.yaml" "$p_file" <<'PY' || { rm -f "$p_file"; fail "platform-shaped render"; }
import sys, yaml
platform = yaml.safe_load(open(sys.argv[1]))
docs = [d for d in yaml.safe_load_all(open(sys.argv[2])) if d]
plugins = {d["plugin"]: d for d in docs if d.get("kind") == "KongPlugin"}
assert sorted(plugins) == ["cors", "opentelemetry", "prometheus"], f"KongPlugins {sorted(plugins)}"
origin = platform["kongPlugins"]["cors"]["origin"]
assert plugins["cors"]["config"]["origins"] == [origin], f"cors origins {plugins['cors']['config']['origins']}, want [{origin!r}]"
traces = platform["kongPlugins"]["opentelemetry"]["tracesEndpoint"]
got = plugins["opentelemetry"]["config"]["traces_endpoint"]
assert got == traces, f"traces_endpoint {got!r}, want {traces!r}"
routes = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "HTTPRoute"}
want_refs = {"amr-fleet-control": platform["gatewayApi"]["parentRefs"],
             "amr-fleet-control-mcp": platform["mcp"]["route"]["parentRefs"],
             "amr-fleet-control-a2a": platform["a2a"]["route"]["parentRefs"]}
assert sorted(routes) == sorted(want_refs), f"HTTPRoutes {sorted(routes)}"
for name, refs in want_refs.items():
    assert routes[name]["spec"]["parentRefs"] == refs, f"{name} parentRefs {routes[name]['spec']['parentRefs']}, want {refs}"
kafka = [d for d in docs if d.get("kind") == "ConfigMap"][0]["data"]["KAFKA_BROKERS"]
assert kafka == platform["kafka"]["brokers"], f"KAFKA_BROKERS {kafka!r}, want the platform's {platform['kafka']['brokers']!r}"
PY
rm -f "$p_file"
# mcp.route.parentRefs / a2a.route.parentRefs fall back to gatewayApi.parentRefs (namespace and sectionName optional).
fb="$(helm template amr-fleet-control "$chart_dir" -f "$fx/app-values.yaml" --set kafka.brokers=k:9092 \
  --set kongPlugins.cors.enabled=false --set kongPlugins.opentelemetry.enabled=false --set 'gatewayApi.parentRefs[0].name=gw' --show-only templates/mcp-route.yaml --show-only templates/a2a-route.yaml)" \
  || fail "routes without their own parentRefs do not render"
python3 -c 'import sys, yaml
refs = [d["spec"]["parentRefs"] for d in yaml.safe_load_all(sys.stdin) if d]
assert refs == [[{"name": "gw"}], [{"name": "gw"}]], refs' <<<"$fb" || fail "mcp/a2a routes do not fall back to gatewayApi.parentRefs"
# ... and a route's own parentRefs win over gatewayApi.parentRefs (the platform file's anchors make them equal).
own="$(helm template amr-fleet-control "$chart_dir" -f "$fx/platform-values.yaml" -f "$fx/app-values.yaml" \
  --set 'mcp.route.parentRefs[0].name=mcp-gw' --set 'a2a.route.parentRefs[0].name=a2a-gw' \
  --show-only templates/mcp-route.yaml --show-only templates/a2a-route.yaml)" || fail "routes with their own parentRefs do not render"
python3 -c 'import sys, yaml
refs = [d["spec"]["parentRefs"] for d in yaml.safe_load_all(sys.stdin) if d]
assert [[r["name"] for r in rs] for rs in refs] == [["mcp-gw"], ["a2a-gw"]], refs' <<<"$own" || fail "mcp/a2a routes ignore their own parentRefs"
# ruling Note C: gatewayApi.enabled gates every HTTPRoute, the mcp and a2a routes too (material-flow-control parity):
# with it off, mcp.route.enabled / a2a.route.enabled render no route.
routes_off="$(helm template amr-fleet-control "$chart_dir" "${all[@]}" --set gatewayApi.enabled=false)" \
  || fail "the all-components render with gatewayApi.enabled=false fails"
[ "$(grep -c '^kind: HTTPRoute' <<<"$routes_off")" = 0 ] \
  || fail "gatewayApi.enabled=false still renders $(grep -c '^kind: HTTPRoute' <<<"$routes_off") HTTPRoute(s)"
[ "$(helm template amr-fleet-control "$chart_dir" "${all[@]}" | grep -c '^kind: HTTPRoute')" = 3 ] \
  || fail "the all-components render must carry the api, mcp and a2a HTTPRoutes"
# The guards: an enabled cors plugin needs an origin (never "*"), an enabled opentelemetry plugin a traces endpoint.
render --set kongPlugins.cors.enabled=true >/dev/null 2>&1 && fail "cors enabled without an origin rendered"
render --set kongPlugins.cors.enabled=true --set kongPlugins.cors.origin='*' >/dev/null 2>&1 && fail "cors origin '*' rendered"
render --set kongPlugins.opentelemetry.enabled=true >/dev/null 2>&1 && fail "opentelemetry enabled without tracesEndpoint rendered"
# An origins list is accepted too (with or without origin), never with "*" in it.
cors="$(render --set kongPlugins.cors.enabled=true --set 'kongPlugins.cors.origins={http://a.invalid,http://b.invalid}' \
  --show-only templates/kong-plugins.yaml)" || fail "cors origins list refused"
python3 -c 'import sys, yaml; o = yaml.safe_load(sys.stdin)["config"]["origins"]; assert o == ["http://a.invalid", "http://b.invalid"], o' \
  <<<"$cors" || fail "cors origins list"
render --set kongPlugins.cors.enabled=true --set 'kongPlugins.cors.origins={http://a.invalid,*}' >/dev/null 2>&1 \
  && fail "cors origins list with '*' rendered"
# ruling P3-R16 / plan 04 P4-R18 (lesson b): ENVIRONMENT (deployment.environment.name on every span and metric) comes
# from the value `environment` (asrs-control's key and default) for api, mcp and a2a, so the cluster never reports "local".
env_of() { # <component> <values...>: the ENVIRONMENT the component's container gets
  local comp="$1"; shift
  if [ "$comp" = api ]; then
    helm template amr-fleet-control "$chart_dir" "${all[@]}" "$@" --show-only templates/configmap.yaml \
      | python3 -c 'import sys, yaml; print(yaml.safe_load(sys.stdin)["data"].get("ENVIRONMENT", "<unset>"))'
  else
    helm template amr-fleet-control "$chart_dir" "${all[@]}" "$@" --show-only "templates/$comp-deployment.yaml" \
      | python3 -c 'import sys, yaml; d = yaml.safe_load(sys.stdin); print(next((e.get("value") for e in d["spec"]["template"]["spec"]["containers"][0]["env"] if e["name"] == "ENVIRONMENT"), "<unset>"))'
  fi
}
for c in api mcp a2a; do
  got="$(env_of "$c")"; [ "$got" = production ] || fail "$c ENVIRONMENT is '$got' by default, want 'production'"
  got="$(env_of "$c" --set environment=staging)"; [ "$got" = staging ] || fail "$c ENVIRONMENT is '$got' with environment=staging"
done
render --set 'extraEnv[0].name=ENVIRONMENT' --set 'extraEnv[0].value=x' >/dev/null 2>&1 && fail "renders an ENVIRONMENT extraEnv"
echo "render_test: PASS"
