# syntax=docker/dockerfile:1

# Minimal artifactsd server image. Builds the static `artifactsd` binary from
# source and ships it on a distroless base (no shell, runs as non-root).
#
#   docker build -t artifactsd .
#   docker buildx build --platform linux/amd64,linux/arm64 -t artifactsd .

FROM golang:1.25-trixie AS build
WORKDIR /src
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
      -ldflags "-s -w -X github.com/jdelic/artifactsd/internal/buildinfo.Version=$VERSION" \
      -o /out/artifactsd ./cmd/artifactsd

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/artifactsd /usr/local/bin/artifactsd

VOLUME ["/data"]
WORKDIR /data
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/artifactsd"]
CMD ["serve", "--listen", ":8080", "--data", "/data"]
