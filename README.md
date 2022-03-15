# ingress

A basic ingress implementation. Started as a hobby project the result is now a small ingress that can
handle basic cases.

## Usage

## Docker container
You can use the ngergs/ingress docker container as a drop-in replacement for other ingress controller
as long as you do not need advanced features. See the [helm](./helm) folder for a corresponding Helm-chart.

### Compilation from Source
Compile from source:
```bash
git clone https://github.com/ngergs/ingress
go build
```

## Usage
The path to this folder has to be provided as command line argument. There are a number of various optional settings.
```
Usage: ./ingress {options} [target-path]
Options:
  -access-log
        Prints an access log. (default true)
  -debug
        Log debug level
  -health
        Whether to start the health check endpoint (/ under a separate port) (default true)
  -health-access-log
        Prints an access log for the health check endpoint to stdout.
  -health-port int
        Different port under which the health check endpoint runs. (default 8081)
  -help
        Prints the help.
  -hsts
        Set HSTS-Header
  -hsts-max-age int
        Max-Age for the HSTS-Header, only relevant if hsts is activated. (default 63072000)
  -hsts-preload
        Whether the HSTS preload directive should be active.
  -hsts-subdomains
        Whether HSTS if activated should add the includeSubdomains directive. (default true)
  -http-port int
        Port for the HTTP endpoint (default 8080)
  -https-port int
        Port for the HTTPs endpoint (default 8443)
  -ingress-class-name string
        Corresponds to spec.ingressClassName. Only ingress definitions that match these are evaluated. (default "ingress")
  -pretty
        Activates zerolog pretty logging
  -read-timeout int
        Timeout to read the entire request in seconds. (default 10)
  -shutdown-timeout int
        Timeout to graceful shutdown the reverse proxy in seconds. (default 10)
  -write-timeout int
        Timeout to write the complete response in seconds. (default 10)
```
