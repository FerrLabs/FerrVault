{{/*
Expand the name of the chart.
*/}}
{{- define "ferrvault-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully-qualified app name. Combines the release name and chart name, truncating
to the 63-char Kubernetes limit.
*/}}
{{- define "ferrvault-operator.fullname" -}}
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
Chart name + version, used in the helm.sh/chart label so `kubectl get` can
tell which chart version produced a given resource.
*/}}
{{- define "ferrvault-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels applied to every resource the chart creates.
*/}}
{{- define "ferrvault-operator.labels" -}}
helm.sh/chart: {{ include "ferrvault-operator.chart" . }}
{{ include "ferrvault-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: ferrvault
{{- end }}

{{/*
Selector labels — narrower set, used for Deployment selectors and Service
selectors so those fields stay immutable across upgrades.
*/}}
{{- define "ferrvault-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "ferrvault-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Name of the ServiceAccount to use. If `serviceAccount.create` is true we mint
a name from the release; otherwise we expect the user to provide one.
*/}}
{{- define "ferrvault-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "ferrvault-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Image tag. Falls back to the chart's appVersion so pinning stays consistent
with the chart version released together.
*/}}
{{- define "ferrvault-operator.imageTag" -}}
{{- default .Chart.AppVersion .Values.image.tag }}
{{- end }}
