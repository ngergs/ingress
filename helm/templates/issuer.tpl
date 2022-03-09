{{- define "issuer" -}} 
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: {{ .name }}
spec:
  acme:
    server: {{ .url }}
    email: {{ .general.email }}
    privateKeySecretRef:
      name: letsencrypt-{{ .name }}
    # Enable the HTTP-01 challenge provider
    solvers:
    - http01:
        ingress:
          class: {{ .general.ingressClassName }}
{{- end }}
