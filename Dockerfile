# syntax=docker/dockerfile:1

# Minimal artifactsd server image. Builds the static `artifactsd` binary from
# source and ships it on a distroless base (no shell, runs as non-root).
#
#   docker build -t artifactsd .
#   docker buildx build --platform linux/amd64,linux/arm64 -t artifactsd .
#
# VERSION, COMMIT and DATE are stamped into the binary (artifactsd version, the
# admin UI footer); the release workflow passes all three as --build-arg.

FROM golang:1.26-trixie AS build
WORKDIR /src
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
      -ldflags "-s -w -X github.com/jdelic/artifactsd/internal/buildinfo.Version=$VERSION \
        -X github.com/jdelic/artifactsd/internal/buildinfo.Commit=$COMMIT \
        -X github.com/jdelic/artifactsd/internal/buildinfo.Date=$DATE" \
      -o /out/artifactsd ./cmd/artifactsd

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/artifactsd /usr/local/bin/artifactsd

VOLUME ["/data"]
WORKDIR /data
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/artifactsd"]
CMD ["serve", "--listen", ":8080", "--data", "/data"]
