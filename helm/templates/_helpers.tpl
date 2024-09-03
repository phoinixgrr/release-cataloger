{{- define "my-service.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "my-service.chart" -}}
{{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}

{{- define "my-service.fullname" -}}
{{ include "my-service.name" . }}-{{ .Release.Name }}
{{- end -}}
