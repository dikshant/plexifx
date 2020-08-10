ARG GO_VERSION=1.14.6
ARG ALPINE_VERSION=3.12
ARG GOPROXY="https://proxy.golang.org,direct"

# Build binary
FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} as builder
LABEL builder=true
ARG GOPROXY
ARG GOPATH
ENV GOPROXY=${GOPROXY} CGO_ENABLED=0
WORKDIR /
ADD . .
RUN go build

# Copy binary
FROM scratch
COPY --from=builder /plexifx /
ENTRYPOINT ["/plexifx"]
