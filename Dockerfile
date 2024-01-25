# Build the manager binary
FROM quay.io/centos/centos:stream9 AS builder
RUN dnf install git golang -y

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# Ensure correct Go version
RUN export GO_VERSION=$(grep -E "go [[:digit:]]\.[[:digit:]][[:digit:]]" go.mod | awk '{print $2}') && \
    go get go@${GO_VERSION} && \
    go version

# Copy the go source
COPY main.go main.go
COPY .git/ .git/
COPY api/ api/
COPY controllers/ controllers/
COPY hack/ hack/
COPY vendor/ vendor/
COPY version/ version/

# Build
RUN ./hack/build.sh .

# Use ubi8 micro as base image to package the manager binary
FROM registry.access.redhat.com/ubi9/ubi-micro:latest
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
