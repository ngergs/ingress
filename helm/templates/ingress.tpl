{{- define "ingress" -}}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ .name }}-master
  annotations:
spec:
  ingressClassName: {{ .ingress_class_name }}
  tls:
  - hosts:
    - {{ .name }}
    secretName: certbot-{{ .common_name }}
  rules:
  - host: {{ .name }}
{{- end }}
