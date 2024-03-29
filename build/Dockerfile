FROM golang:1.22.0-alpine as build-container
ARG VERSION=snapshot

COPY . /root/app
WORKDIR /root

RUN apk --no-cache add git && \
  go install github.com/google/go-licenses@latest && \
  cd app && \
  CGO_ENABLED=0 GOOD=linux GOARCH=amd64 go build -a -ldflags "-s -w -X 'main.version=${VERSION}'" ./cmd/ingress/ && \
  go-licenses save ./... --save_path=legal

FROM gcr.io/distroless/static:nonroot
COPY --from=build-container /root/app/ingress /app/ingress
COPY --from=build-container /root/app/legal /app/legal
EXPOSE 8080 8443 8081
ENTRYPOINT ["/app/ingress"]
CMD []
