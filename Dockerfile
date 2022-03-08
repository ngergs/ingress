FROM golang:1.17-alpine as build-container
COPY . /root/
WORKDIR /root
RUN CGO_ENABLED=0 GOOD=linux GOARCH=amd64 go build -a -ldflags '-s -w' && \
go get github.com/google/go-licenses && \
go-licenses save ./... --save_path=legal

FROM gcr.io/distroless/static:debug
COPY --from=build-container --chown=nobody:nobody /root/ingress /app/ingress
COPY --from=build-container --chown=nobody:nobody /root/legal /app/legal
USER nobody
EXPOSE 8080 8443 8081
ENTRYPOINT ["/app/ingress"]
CMD []
