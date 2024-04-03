FROM gcr.io/distroless/static:nonroot
COPY ingress /app/ingress
COPY legal /app/legal
USER nobody
EXPOSE 8080 8081 8443
ENTRYPOINT ["/app/ingress"]
CMD []
