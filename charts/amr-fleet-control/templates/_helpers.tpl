{{- define "amr-fleet-control.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "amr-fleet-control.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "amr-fleet-control.mcpFullname" -}}
{{- printf "%s-mcp" (include "amr-fleet-control.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "amr-fleet-control.a2aFullname" -}}
{{- printf "%s-a2a" (include "amr-fleet-control.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "amr-fleet-control.selectorLabels" -}}
app.kubernetes.io/name: {{ include "amr-fleet-control.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "amr-fleet-control.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "amr-fleet-control.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "amr-fleet-control.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "amr-fleet-control.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "amr-fleet-control.image" -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) }}
{{- end }}

{{- define "amr-fleet-control.serviceVersion" -}}
{{- .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{- define "amr-fleet-control.databaseSecretName" -}}
{{- default (printf "%s-db" (include "amr-fleet-control.fullname" .)) .Values.database.existingSecret }}
{{- end }}

{{/* The comma-separated KongPlugin names attached to every route; empty when none is enabled. */}}
{{- define "amr-fleet-control.kongPluginNames" -}}
{{- $names := list -}}
{{- range $name, $cfg := .Values.kongPlugins -}}
{{- if $cfg.enabled -}}
{{- $names = append $names (printf "%s-%s" (include "amr-fleet-control.fullname" $) $name) -}}
{{- end -}}
{{- end -}}
{{- join "," $names -}}
{{- end -}}

{{/* The database env every binary reads (each runs the idempotent migrations at boot). */}}
{{- define "amr-fleet-control.databaseEnv" -}}
- name: DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: {{ include "amr-fleet-control.databaseSecretName" . }}
      key: {{ .Values.database.existingSecretKey }}
{{- if .Values.database.migrationsExistingSecretKey }}
- name: MIGRATIONS_DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: {{ include "amr-fleet-control.databaseSecretName" . }}
      key: {{ .Values.database.migrationsExistingSecretKey }}
      optional: true
{{- end }}
{{- end }}

{{/* The map volume: the ConfigMap's map.yaml at config.mapFile, mounted by every component. */}}
{{- define "amr-fleet-control.mapVolume" -}}
- name: map
  configMap:
    name: {{ include "amr-fleet-control.fullname" . }}
    items:
      - key: map.yaml
        path: {{ base .Values.config.mapFile }}
{{- end }}

{{/* Mandatory values: real Postgres, Kafka and MQTT only, one api replica. */}}
{{- define "amr-fleet-control.require" -}}
{{- if and (not .Values.database.url) (not .Values.database.existingSecret) }}
{{- fail "database.url or database.existingSecret is required: amr-fleet-control runs on Postgres only" }}
{{- end }}
{{- if not .Values.kafka.brokers }}
{{- fail "kafka.brokers is required: the outbox relay publishes to Kafka" }}
{{- end }}
{{- if not .Values.mqtt.url }}
{{- fail "mqtt.url is required: the fleet link speaks VDA 5050 over MQTT" }}
{{- end }}
{{- if ne (int .Values.replicaCount) 1 }}
{{- fail "replicaCount must be 1: amr-fleet-control is the single traffic controller of its fleet (ADR 0002)" }}
{{- end }}
{{- if lt (int .Values.terminationGracePeriodSeconds) 15 }}
{{- fail "terminationGracePeriodSeconds must be at least 15 (10 s HTTP drain plus the relay and MQTT shutdown)" }}
{{- end }}
{{- range .Values.extraEnv }}
{{- if or (hasSuffix "_MODE" .name) (eq .name "OTEL_EXPORTER_OTLP_ENDPOINT") (eq .name "OTEL_SERVICE_NAME") (eq .name "DATABASE_URL") }}
{{- fail (printf "extraEnv must not set %s: the chart renders it (or it does not exist)" .name) }}
{{- end }}
{{- end }}
{{- end }}
