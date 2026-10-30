{{- define "receipts.secret" -}}{{ .Release.Name }}-app-secret{{- end -}}
{{- define "receipts.dbHost" -}}{{ .Release.Name }}-postgres{{- end -}}
{{/* DATABASE_URL built from DB_PASSWORD at runtime by kubelet $(VAR) expansion; generate db-password with openssl rand -hex so it is URL-safe. */}}
{{- define "receipts.dbEnv" -}}
- name: DB_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ include "receipts.secret" . }}
      key: db-password
- name: DATABASE_URL
  value: "postgres://{{ .Values.postgres.user }}:$(DB_PASSWORD)@{{ include "receipts.dbHost" . }}:5432/{{ .Values.postgres.database }}?sslmode=disable"
{{- end -}}
{{- define "receipts.podSecurity" -}}
runAsNonRoot: true
runAsUser: 1000
runAsGroup: 1000
fsGroup: 1000
seccompProfile:
  type: RuntimeDefault
{{- end -}}
{{- define "receipts.containerSecurity" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop: ["ALL"]
{{- end -}}
