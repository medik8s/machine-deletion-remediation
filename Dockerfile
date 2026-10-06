# Build the manager binary
FROM quay.io/konveyor/builder:ubi9-latest AS builder
ARG TARGETOS
ARG TARGETARCH
ARG OPERATOR_VERSION=""

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.sum ./
COPY Makefile Makefile
ENV GOTOOLCHAIN=auto

# Copy the go source
COPY cmd/ cmd/
COPY .git/ .git/
COPY api/ api/
COPY internal/ internal/
COPY hack/ hack/
COPY vendor/ vendor/
COPY version/ version/

RUN go version
RUN git config --global --add safe.directory /workspace
# Do not inherit the builder image's generic VERSION environment variable.
RUN VERSION="${OPERATOR_VERSION}" ./hack/build.sh bin

# Use ubi9 micro as base image to package the manager binary
FROM registry.access.redhat.com/ubi9/ubi-micro:latest
WORKDIR /
COPY --from=builder /workspace/bin/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
