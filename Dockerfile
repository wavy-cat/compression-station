FROM --platform=$BUILDPLATFORM golang:1.26.3 AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src/server

COPY go* .
RUN apt update && apt install gcc zstd -y && go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o main github.com/wavy-cat/compression-station/cmd/server

FROM gcr.io/distroless/base-debian13

LABEL authors="WavyCat"
LABEL org.opencontainers.image.source="https://github.com/wavy-cat/compression-station"

WORKDIR /server
COPY --from=builder /src/server /server

ENTRYPOINT ["./main"]