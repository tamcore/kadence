{{/*
Expand the name of the chart.
*/}}
{{- define "kadence.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "kadence.fullname" -}}
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

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "kadence.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kadence.labels" -}}
helm.sh/chart: {{ include "kadence.chart" . }}
app.kubernetes.io/name: {{ include "kadence.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app: {{ include "kadence.name" . }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kadence.selectorLabels" -}}
app: {{ include "kadence.name" . }}
{{- end }}

{{/*
Postgres StatefulSet/Service labels and selector labels.
*/}}
{{- define "kadence.postgres.labels" -}}
{{- include "kadence.labels" . }}
app.kubernetes.io/component: postgres
{{- end }}

{{- define "kadence.postgres.selectorLabels" -}}
{{- include "kadence.selectorLabels" . }}
app.kubernetes.io/component: postgres
{{- end }}

{{/*
MCP server fully qualified name: <release>-mcp-<serverName>.
Context: dict "root" $ "server" $server
*/}}
{{- define "kadence.mcp.fullname" -}}
{{- printf "%s-mcp-%s" (include "kadence.fullname" .root) .server.name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
MCP server scope token: GLOBAL or USER_<username>.
Context: dict "root" $ "server" $server
*/}}
{{- define "kadence.mcp.scopeToken" -}}
{{- if kindIs "map" .server.scope -}}USER_{{ .server.scope.user }}{{- else if eq (toString .server.scope) "global" -}}GLOBAL{{- else -}}{{ fail (printf "mcp server %s: scope must be 'global' or {user: <name>}" .server.name) }}{{- end -}}
{{- end -}}

{{/*
MCP env var prefix: MCP_<UPPER_NAME>_<SCOPE>. Fails if server name contains "_"
(would corrupt internal/mcp/env.go's MCP_<NAME>_<SCOPE>_<FIELD> parser).
Context: dict "root" $ "server" $server
*/}}
{{- define "kadence.mcp.envPrefix" -}}
{{- if contains "_" .server.name }}{{ fail (printf "mcp server name %q must not contain '_'" .server.name) }}{{- end -}}
{{- printf "MCP_%s_%s" (upper .server.name) (include "kadence.mcp.scopeToken" .) -}}
{{- end -}}

{{/*
Global sticky MCP basicAuth Secret name: <release>-mcp-auth.
Context: the root context (.), NOT a dict — shared by all servers.
*/}}
{{- define "kadence.mcp.authSecretName" -}}
{{- printf "%s-mcp-auth" (include "kadence.fullname" .) -}}
{{- end -}}

{{/*
markitdown-mcp fully qualified name: <release>-markitdown.
Context: the root context (.).
*/}}
{{- define "kadence.markitdown.fullname" -}}
{{- printf "%s-markitdown" (include "kadence.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Sticky markitdown basicAuth Secret name: <release>-markitdown-auth.
Context: the root context (.).
*/}}
{{- define "kadence.markitdown.authSecretName" -}}
{{- printf "%s-markitdown-auth" (include "kadence.fullname" .) -}}
{{- end -}}

{{/*
Database env vars (KADENCE_DATABASE_URL and, when using the bundled
Postgres, POSTGRES_PASSWORD). Shared by the app container and the
wait-for-db initContainer so both use the same one source of truth.
Context: the root context (.).
*/}}
{{- define "kadence.dbEnv" -}}
{{- if .Values.postgres.enabled }}
- name: POSTGRES_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ include "kadence.fullname" . }}-secret
      key: POSTGRES_PASSWORD
- name: KADENCE_DATABASE_URL
  value: "postgres://{{ .Values.postgres.username }}:$(POSTGRES_PASSWORD)@{{ include "kadence.fullname" . }}-postgres:5432/{{ .Values.postgres.database }}?sslmode=disable"
{{- else if .Values.externalDatabase.existingSecret }}
- name: KADENCE_DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: {{ .Values.externalDatabase.existingSecret }}
      key: {{ .Values.externalDatabase.existingSecretKey | default "KADENCE_DATABASE_URL" }}
{{- else }}
- name: KADENCE_DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: {{ include "kadence.fullname" . }}-secret
      key: KADENCE_DATABASE_URL
{{- end }}
{{- end -}}
