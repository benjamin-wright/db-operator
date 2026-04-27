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
{{- end }}

{{- define "db-mcp.fullname" -}}
{{- printf "%s-mcp" .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "db-mcp.selectorLabels" -}}
app.kubernetes.io/name: {{ include "db-mcp.fullname" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "db-mcp.labels" -}}
helm.sh/chart: {{ include "db-operator.chart" . }}
{{ include "db-mcp.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}
