{{- define "db-mcp.fullname" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "db-mcp.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "db-mcp.labels" -}}
helm.sh/chart: {{ include "db-mcp.chart" . }}
{{ include "db-mcp.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "db-mcp.selectorLabels" -}}
app.kubernetes.io/name: {{ include "db-mcp.fullname" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
