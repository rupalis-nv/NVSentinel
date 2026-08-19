{{/*
MongoDB PVC / manual-PV size from the backend-native values (issue #1601).
Do not invent a global override — Bitnami and Percona already expose size
through their own keys, and useBitnami / usePerconaOperator are mutually exclusive.
*/}}
{{- define "mongodb-store.persistenceSize" -}}
{{- if .Values.usePerconaOperator -}}
{{- index .Values "psmdb-db" "replsets" "rs0" "volumeSpec" "pvc" "resources" "requests" "storage" | default "8Gi" -}}
{{- else -}}
{{- .Values.mongodb.persistence.size | default "8Gi" -}}
{{- end -}}
{{- end }}

{{/*
Validated HealthEvents/MaintenanceEvents TTL expireAfterSeconds.
Nil/empty defaults to 2592000 (30 days) before int-cast: sprig int(nil) is 0,
and expireAfterSeconds: 0 makes MongoDB delete matching documents immediately.
Explicit 0 is still allowed. Range is 0–2147483647 (MongoDB int32).
*/}}
{{- define "mongodb-store.collectionExpirySeconds" -}}
{{- $raw := .Values.collectionExpirySeconds -}}
{{- if or (kindIs "invalid" $raw) (eq ($raw | toString) "") -}}
{{- $raw = 2592000 -}}
{{- end -}}
{{- $v := int $raw -}}
{{- if or (lt $v 0) (gt $v 2147483647) -}}
{{- fail (printf "mongodb-store.collectionExpirySeconds must be an integer from 0 through 2147483647, got %v" $raw) -}}
{{- end -}}
{{- $v -}}
{{- end }}

{{/*
Setup Job name. Includes the TTL so Helm/ArgoCD treat a collectionExpirySeconds
change as a new Job (create) instead of patching annotations on a completed Job.
*/}}
{{- define "mongodb-store.initJobName" -}}
{{- printf "create-mongodb-database-%s" (include "mongodb-store.collectionExpirySeconds" . | toString) | trunc 63 -}}
{{- end }}

{{/*
Expand the name of the chart.
*/}}
{{- define "nvsentinel.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "nvsentinel.fullname" -}}
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
{{- define "nvsentinel.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "nvsentinel.labels" -}}
helm.sh/chart: {{ include "nvsentinel.chart" . }}
{{ include "nvsentinel.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "nvsentinel.selectorLabels" -}}
app.kubernetes.io/name: {{ include "nvsentinel.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "nvsentinel.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "nvsentinel.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}
