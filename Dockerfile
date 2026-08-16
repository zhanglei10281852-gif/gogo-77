# Multi-stage build for the FluWatershed CLI.
#
# The builder stage compiles a fully static binary offline. GOTOOLCHAIN=local
# forbids downloading a different Go toolchain, GOPROXY=off forbids fetching
# modules, and CGO_ENABLED=0 keeps the result free of dynamic linking. The
# module depends on nothing outside the Go standard library, so all three hold.
#
# The final stage is scratch and contains only the binary.
FROM golang:1.22 AS builder

ENV GOTOOLCHAIN=local \
    CGO_ENABLED=0 \
    GOPROXY=off \
    GOFLAGS=-mod=mod

WORKDIR /src

COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal

RUN go vet ./... \
 && go build -trimpath -ldflags "-s -w" -o /out/fluwatershed ./cmd/fluwatershed

FROM scratch

COPY --from=builder /out/fluwatershed /fluwatershed

ENTRYPOINT ["/fluwatershed"]
