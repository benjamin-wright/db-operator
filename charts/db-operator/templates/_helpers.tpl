{{/*
Operator naming and labels.
*/}}
{{- define "db-operator.fullname" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "db-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "db-operator.labels" -}}
helm.sh/chart: {{ include "db-operator.chart" . }}
{{ include "db-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "db-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "db-operator.fullname" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: operator
{{- end }}

{{/*
Resolve the operator image, defaulting tag to .Chart.AppVersion.
*/}}
{{- define "db-operator.image" -}}
{{- $tag := .Values.image.tag | default (printf "v%s" .Chart.AppVersion) -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end }}

{{/*
Resolve the migration image, defaulting tag to .Chart.AppVersion.
*/}}
{{- define "db-operator.migrationImage" -}}
{{- $tag := .Values.migrationImage.tag | default (printf "v%s" .Chart.AppVersion) -}}
{{- printf "%s:%s" .Values.migrationImage.repository $tag -}}
{{- end }}

{{/*
MCP naming and labels.
*/}}
{{- define "db-operator.mcp.fullname" -}}
{{- printf "%s-mcp" (include "db-operator.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "db-operator.mcp.labels" -}}
helm.sh/chart: {{ include "db-operator.chart" . }}
{{ include "db-operator.mcp.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "db-operator.mcp.selectorLabels" -}}
app.kubernetes.io/name: {{ include "db-operator.mcp.fullname" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: mcp
{{- end }}

{{/*
Resolve the MCP image, defaulting tag to .Chart.AppVersion.
*/}}
{{- define "db-operator.mcp.image" -}}
{{- $tag := .Values.mcp.image.tag | default (printf "v%s" .Chart.AppVersion) -}}
{{- printf "%s:%s" .Values.mcp.image.repository $tag -}}
{{- end }}
