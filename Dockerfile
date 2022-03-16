FROM golang:1.18-alpine as build-container
COPY . /root/app
WORKDIR /root

RUN apk --no-cache add git && \
  go install github.com/google/go-licenses@latest && \
  cd app && \
  CGO_ENABLED=0 GOOD=linux GOARCH=amd64 go build -a -ldflags '-s -w' && \
  go-licenses save ./... --save_path=legal

FROM gcr.io/distroless/static:debug
COPY --from=build-container --chown=nobody:nobody /root/app/ingress /app/ingress
COPY --from=build-container --chown=nobody:nobody /root/app/legal /app/legal
USER nobody
EXPOSE 8080 8443 8081
ENTRYPOINT ["/app/ingress"]
CMD []
