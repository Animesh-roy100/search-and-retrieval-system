{{/*
Substitute {registry} and {tag} placeholders in an app image string.
Usage: {{ include "srs.image" (dict "ctx" $ "image" .Values.query.image) }}
*/}}
{{- define "srs.image" -}}
{{- .image | replace "{registry}" .ctx.Values.global.appRegistry | replace "{tag}" .ctx.Values.global.appTag -}}
{{- end -}}

{{/* Common labels for a component. Usage: {{ include "srs.labels" "query" | nindent 4 }} */}}
{{- define "srs.labels" -}}
app: {{ . }}
app.kubernetes.io/name: {{ . }}
app.kubernetes.io/part-of: search-and-retrieval
{{- end -}}
