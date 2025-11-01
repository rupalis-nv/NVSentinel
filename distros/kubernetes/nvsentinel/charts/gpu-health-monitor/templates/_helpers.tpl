{{/*
Expand the name of the chart.
*/}}
{{- define "gpu-health-monitor.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "gpu-health-monitor.fullname" -}}
{{- "gpu-health-monitor" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "gpu-health-monitor.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "gpu-health-monitor.labels" -}}
helm.sh/chart: {{ include "gpu-health-monitor.chart" . }}
{{ include "gpu-health-monitor.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "gpu-health-monitor.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gpu-health-monitor.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}