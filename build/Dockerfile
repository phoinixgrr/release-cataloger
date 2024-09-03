# This Dockerfile is used to build the release-cataloger application
# A multi-stage build with Golang as the builder
# and Ubuntu LTS as the runner

# Set the Go version as an argument
ARG GO_VERSION=1.22.6

# Stage 1: Build the application
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS builder
ARG TARGETOS
ARG TARGETARCH
ARG GO_BUILD_PLATFORMS=${TARGETOS}-${TARGETARCH}

# Copy the source code to the build container
COPY . /src

# Set the working directory inside the container
WORKDIR /src

# Set the environment to production and Build the application
RUN GIN_MODE=release make go-build

# Stage 2: Create the final image using ubuntu LTS
FROM --platform=$TARGETPLATFORM ubuntu:24.04@sha256:c920ba4cfca05503764b785c16b76d43c83a6df8d1ab107e7e6610000d94315c AS runner
# hadolint ignore=DL3008
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install --no-install-recommends -y ca-certificates \
  && apt-get clean \
  && rm -rf /var/lib/apt/lists/*

ARG TARGETOS
ARG TARGETARCH
ARG PUID=2000
ARG PGID=2000

# Copy the binary from the builder stage
COPY --from=builder /src/dist/release-cataloger-${TARGETOS}-${TARGETARCH} /opt/release-cataloger/bin/release-cataloger
# Copy the entrypoint script
COPY build/entrypoint.sh /opt/release-cataloger/bin/entrypoint.sh

# We should refrain from running as privileged user
RUN groupadd --gid ${PGID} cataloger \
  && useradd --uid ${PUID} --gid ${PGID} --comment "" --home-dir /cataloger cataloger \
  && chmod +x /opt/release-cataloger/bin/entrypoint.sh \
  && chown -R cataloger:cataloger /opt/release-cataloger

USER cataloger

# Set the working directory inside the final container
WORKDIR /opt/release-cataloger/bin

# Set the entrypoint to the script
ENTRYPOINT ["./entrypoint.sh", "./release-cataloger"]
