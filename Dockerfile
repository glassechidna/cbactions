FROM golang:1.15 AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

ENV CGO_ENABLED=0

COPY . .

WORKDIR entrypoint
RUN go build -ldflags="-s -w"

WORKDIR ../mux
RUN go build -ldflags="-s -w"

WORKDIR ../worker
RUN go build -ldflags="-s -w"

# using 1.13 because of SIGURG issues. TODO investigate why
FROM golang:1.13 AS preload
WORKDIR /preload
COPY . .
RUN go build -buildmode=c-shared -o preload.so -ldflags="-s -w" ./preload

FROM ubuntu AS base
RUN apt-get update && apt-get install -y curl git libicu60
WORKDIR /runner
RUN curl -o runner.tgz -L https://github.com/actions/runner/releases/download/v2.273.5/actions-runner-linux-x64-2.273.5.tar.gz && \
    tar xvf runner.tgz && \
    rm runner.tgz

RUN mkdir -p _diag _work && chmod 0777 _diag _work

FROM base AS listener
COPY --from=preload /preload/preload.so /runner/preload.so
COPY --from=builder /build//worker/worker /runner/bin/Runner.Worker
COPY --from=builder /build/mux/mux /usr/bin
CMD mux

FROM base AS worker
COPY --from=builder /build/entrypoint /usr/bin
CMD entrypoint
