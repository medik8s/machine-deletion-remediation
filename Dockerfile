# Build the manager binary
FROM quay.io/centos/centos:stream8 AS builder
RUN dnf install git golang -y

# Ensure go 1.15
RUN go install golang.org/dl/go1.15@latest
RUN ~/go/bin/go1.15 download
RUN /bin/cp -f ~/go/bin/go1.15 /usr/bin/go
RUN go version

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# Copy the go source
COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/
COPY vendor/ vendor/

# Build
RUN GOFLAGS=-mod=vendor CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o manager main.go

# Use ubi8 as minimal base image to package the manager binary
FROM registry.access.redhat.com/ubi8/ubi-minimal
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
