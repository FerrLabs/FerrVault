{{/*
Expand the name of the chart.
*/}}
{{- define "ferrflow-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully-qualified app name. Combines the release name and chart name, truncating
to the 63-char Kubernetes limit.
*/}}
{{- define "ferrflow-operator.fullname" -}}
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
{{- define "ferrflow-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels applied to every resource the chart creates.
*/}}
{{- define "ferrflow-operator.labels" -}}
helm.sh/chart: {{ include "ferrflow-operator.chart" . }}
{{ include "ferrflow-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: ferrflow
{{- end }}

{{/*
Selector labels — narrower set, used for Deployment selectors and Service
selectors so those fields stay immutable across upgrades.
*/}}
{{- define "ferrflow-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "ferrflow-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Name of the ServiceAccount to use. If `serviceAccount.create` is true we mint
a name from the release; otherwise we expect the user to provide one.
*/}}
{{- define "ferrflow-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "ferrflow-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Image tag. Falls back to the chart's appVersion so pinning stays consistent
with the chart version released together.
*/}}
{{- define "ferrflow-operator.imageTag" -}}
{{- default .Chart.AppVersion .Values.image.tag }}
{{- end }}
