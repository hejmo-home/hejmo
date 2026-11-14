# syntax=docker/dockerfile:1
# Build: pure Go (CGO off), cross-compiled for the target platform.
FROM --platform=$BUILDPLATFORM golang:1.27.4 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/hejmo ./cmd/hejmo \
 && mkdir -p /out/data

# Runtime: distroless static (Apache-2.0), no shell, runs as non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/hejmo /hejmo
COPY --from=build --chown=65532:65532 /out/data /data
ENV HEJMO_ADDR=:8080 HEJMO_HTTPS_ADDR=:8443 HEJMO_DATA_DIR=/data
VOLUME /data
EXPOSE 8080 8443
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/hejmo", "healthcheck"]
ENTRYPOINT ["/hejmo"]
