{{/*
Manual-PV size: mongodb.persistence.size, or Percona rs0 volumeSpec when usePerconaOperator.
*/}}
{{- define "mongodb-store.persistenceSize" -}}
{{- if .Values.usePerconaOperator -}}
{{- index .Values "psmdb-db" "replsets" "rs0" "volumeSpec" "pvc" "resources" "requests" "storage" | default "8Gi" -}}
{{- else -}}
{{- .Values.mongodb.persistence.size | default "8Gi" -}}
{{- end -}}
{{- end }}

{{/*
TTL expireAfterSeconds. Nil/empty → 2592000. int(nil)/int("abc") is 0 (immediate expiry).
Strings must be base-10 digits; range 0–2147483647 is checked before int for strings.
*/}}
{{- define "mongodb-store.collectionExpirySeconds" -}}
{{- $raw := .Values.collectionExpirySeconds -}}
{{- if or (kindIs "invalid" $raw) (eq ($raw | toString) "") -}}
{{- $raw = 2592000 -}}
{{- end -}}
{{- if kindIs "string" $raw -}}
{{- if not (regexMatch "^[0-9]+$" $raw) -}}
{{- fail (printf "mongodb-store.collectionExpirySeconds must be an integer from 0 through 2147483647, got %v" $raw) -}}
{{- end -}}
{{- if or (gt (len $raw) 10) (and (eq (len $raw) 10) (gt $raw "2147483647")) -}}
{{- fail (printf "mongodb-store.collectionExpirySeconds must be an integer from 0 through 2147483647, got %v" $raw) -}}
{{- end -}}
{{- end -}}
{{- $v := int $raw -}}
{{- if or (lt $v 0) (gt $v 2147483647) -}}
{{- fail (printf "mongodb-store.collectionExpirySeconds must be an integer from 0 through 2147483647, got %v" $raw) -}}
{{- end -}}
{{- $v -}}
{{- end }}

{{/*
Parse a Kubernetes quantity to integer MiB (bytes / 1048576).
Binary suffixes (Ki/Mi/Gi/Ti) use 1024; decimal SI (K/M/G/T) use 1000.
Coefficient must be an integer.
*/}}
{{- define "mongodb-store.storageToMB" -}}
{{- $s := . | toString | trim -}}
{{- $coef := "" -}}
{{- $bytesPer := 0 -}}
{{- if hasSuffix "Ti" $s -}}
{{- $coef = trimSuffix "Ti" $s -}}{{- $bytesPer = 1099511627776 -}}
{{- else if hasSuffix "Gi" $s -}}
{{- $coef = trimSuffix "Gi" $s -}}{{- $bytesPer = 1073741824 -}}
{{- else if hasSuffix "Mi" $s -}}
{{- $coef = trimSuffix "Mi" $s -}}{{- $bytesPer = 1048576 -}}
{{- else if hasSuffix "Ki" $s -}}
{{- $coef = trimSuffix "Ki" $s -}}{{- $bytesPer = 1024 -}}
{{- else if hasSuffix "T" $s -}}
{{- $coef = trimSuffix "T" $s -}}{{- $bytesPer = 1000000000000 -}}
{{- else if hasSuffix "G" $s -}}
{{- $coef = trimSuffix "G" $s -}}{{- $bytesPer = 1000000000 -}}
{{- else if hasSuffix "M" $s -}}
{{- $coef = trimSuffix "M" $s -}}{{- $bytesPer = 1000000 -}}
{{- else if hasSuffix "K" $s -}}
{{- $coef = trimSuffix "K" $s -}}{{- $bytesPer = 1000 -}}
{{- else -}}
{{- fail (printf "mongodb-store persistence size %q must have a unit (Gi, G, Mi, M, Ti, T)" $s) -}}
{{- end -}}
{{- if not (regexMatch "^[0-9]+$" $coef) -}}
{{- fail (printf "mongodb-store persistence size %q must use an integer coefficient" $s) -}}
{{- end -}}
{{- $bytes := mul (int $coef) $bytesPer -}}
{{- $mb := div $bytes 1048576 -}}
{{- if and (eq $mb 0) (gt $bytes 0) -}}{{- $mb = 1 -}}{{- end -}}
{{- $mb -}}
{{- end }}

{{/*
Oplog MB = persistenceSize × oplogPercentOfVolume, floored to MongoDB's 990 MiB
minimum and capped at 50% of the volume so data files still fit.
Nil/empty percent → 10. Range 1–50.
*/}}
{{- define "mongodb-store.oplogSizeMB" -}}
{{- $raw := .Values.oplogPercentOfVolume -}}
{{- if or (kindIs "invalid" $raw) (eq ($raw | toString) "") -}}
{{- $raw = 10 -}}
{{- end -}}
{{- $s := $raw | toString -}}
{{- if not (regexMatch "^[0-9]+$" $s) -}}
{{- fail (printf "mongodb-store.oplogPercentOfVolume must be an integer from 1 through 50, got %v" $raw) -}}
{{- end -}}
{{- $pct := int $s -}}
{{- if or (lt $pct 1) (gt $pct 50) -}}
{{- fail (printf "mongodb-store.oplogPercentOfVolume must be an integer from 1 through 50, got %v" $raw) -}}
{{- end -}}
{{- $volMB := include "mongodb-store.storageToMB" (include "mongodb-store.persistenceSize" .) | int -}}
{{- $mb := div (mul $volMB $pct) 100 -}}
{{- if lt $mb 990 -}}
{{- $mb = 990 -}}
{{- end -}}
{{- $half := div $volMB 2 -}}
{{- if lt $half 990 -}}
{{- fail (printf "data volume %d MB cannot reserve 990 MB for oplog and retain 50%% for data; increase mongodb persistence size" $volMB) -}}
{{- end -}}
{{- if gt $mb $half -}}
{{- $mb = $half -}}
{{- end -}}
{{- if ge $mb $volMB -}}
{{- fail (printf "computed oplog %d MB does not fit data volume %d MB; increase mongodb persistence size" $mb $volMB) -}}
{{- end -}}
{{- $mb -}}
{{- end }}

{{/*
Direct-connection hostnames for replSetResizeOplog (must run on every member).
*/}}
{{- define "mongodb-store.oplogMemberHosts" -}}
{{- $ns := .Release.Namespace -}}
{{- $hosts := list -}}
{{- if .Values.usePerconaOperator -}}
{{- $n := index .Values "psmdb-db" "replsets" "rs0" "size" | default 3 | int -}}
{{- range $i := until $n -}}
{{- $hosts = append $hosts (printf "mongodb-rs0-%d.mongodb-rs0.%s.svc.cluster.local" $i $ns) -}}
{{- end -}}
{{- else -}}
{{- $n := .Values.mongodb.replicaCount | default 3 | int -}}
{{- range $i := until $n -}}
{{- $hosts = append $hosts (printf "mongodb-%d.mongodb-headless.%s.svc.cluster.local" $i $ns) -}}
{{- end -}}
{{- end -}}
{{- join " " $hosts -}}
{{- end }}

{{/*
create-mongodb-database-<ttl>-<scriptHash> so a TTL, oplog size, or init-script
change is a new Job, not a patch on a completed one.
*/}}
{{- define "mongodb-store.initJobName" -}}
{{- $ttl := include "mongodb-store.collectionExpirySeconds" . | toString -}}
{{- $hash := printf "%s\n%s\n%s\n%s" (include "mongodb-store.oplogSizeMB" .) (include "mongodb-store.oplogMemberHosts" .) (include "mongodb-store.initEval" .) (include "mongodb-store.oplogEval" .) | sha256sum | trunc 8 -}}
{{- printf "create-mongodb-database-%s-%s" $ttl $hash | trunc 63 -}}
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
