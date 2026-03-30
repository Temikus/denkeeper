{{/*
Validate required configuration fields at install time.
These mirror the runtime checks in internal/config/config.go:validate().
*/}}

{{- define "denkeeper.validate" -}}

{{/* Telegram: allowed_users required when token is set */}}
{{- if .Values.secrets.telegramToken }}
  {{- if not (dig "telegram" "allowed_users" nil .Values.config) }}
    {{- fail "config.telegram.allowed_users must be set when secrets.telegramToken is provided (security requirement)" }}
  {{- end }}
  {{- if eq (len (dig "telegram" "allowed_users" list .Values.config)) 0 }}
    {{- fail "config.telegram.allowed_users must not be empty when secrets.telegramToken is provided (security requirement)" }}
  {{- end }}
{{- end }}

{{/* Discord: allowed_users required when token is set */}}
{{- if .Values.secrets.discordToken }}
  {{- if not (dig "discord" "allowed_users" nil .Values.config) }}
    {{- fail "config.discord.allowed_users must be set when secrets.discordToken is provided (security requirement)" }}
  {{- end }}
  {{- if eq (len (dig "discord" "allowed_users" list .Values.config)) 0 }}
    {{- fail "config.discord.allowed_users must not be empty when secrets.discordToken is provided (security requirement)" }}
  {{- end }}
{{- end }}

{{/* Telegram allowed_users entries must be integers, not floats or strings */}}
{{- if (dig "telegram" "allowed_users" nil .Values.config) }}
  {{- range $user := dig "telegram" "allowed_users" list .Values.config }}
    {{- if not (kindIs "float64" $user) }}
      {{- if not (kindIs "int64" $user) }}
        {{- if not (kindIs "int" $user) }}
          {{- fail (printf "config.telegram.allowed_users entries must be integers, got %v (%s)" $user (kindOf $user)) }}
        {{- end }}
      {{- end }}
    {{- end }}
  {{- end }}
{{- end }}

{{/* At least one adapter must be configured (skip when using existingSecret — we can't inspect its contents) */}}
{{- if not .Values.existingSecret }}
  {{- if and (not .Values.secrets.telegramToken) (not .Values.secrets.discordToken) }}
    {{- fail "at least one adapter must be configured: set secrets.telegramToken or secrets.discordToken (or use existingSecret)" }}
  {{- end }}
{{- end }}

{{- end -}}
