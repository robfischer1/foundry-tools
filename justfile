# foundry-tools — the fleet's checks, as code.

# Regenerate the SDK bindings, then undo the SDK's own vulnerable pin.
#
# `dagger develop` re-adds four `replace` directives holding
# go.opentelemetry.io/otel/exporters/otlp/otlplog/* , otel/log and otel/sdk/log
# at v0.16.0, and v0.16.0 carries GO-2026-4985. Dropping them and re-tidying
# builds, tests and loads identically. A module whose whole point is that a
# check must not be silenced does not silence its own.
develop:
    dagger develop
    go mod edit \
      -dropreplace=go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp \
      -dropreplace=go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc \
      -dropreplace=go.opentelemetry.io/otel/log \
      -dropreplace=go.opentelemetry.io/otel/sdk/log
    go mod tidy

# What CI runs. Same commands, same order.
#
# Package main's tests run against a paper engine (engine_fake_test.go); the
# session it needs is defaulted by internal/checks/session.go, so nothing here
# has to export it.
gate:
    test -z "$(gofmt -l . )"
    go vet ./...
    go build ./...
    go test -race ./...
    staticcheck ./...
    govulncheck ./...

# List the atoms this module carries.
atoms:
    dagger check -l
