{{- define "ingress_robot" -}}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: robots-{{ .name }}
  annotations:
    nginx.org/mergeable-ingress-type: minion
spec:
  ingressClassName: {{ .ingress_class_name }}
  rules:
  - host: {{ .name }}
    http:
      paths:
      - path: /robots.txt
        pathType: Exact
        backend:
          service:
            name: robots-{{ .common_name | replace "." "-" }}
            port:
              number: 8080
{{- end }}
