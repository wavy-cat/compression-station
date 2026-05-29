FROM --platform=$BUILDPLATFORM golang:1.26 AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src/server

COPY go* .
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o main github.com/wavy-cat/compression-station/cmd/server

FROM gcr.io/distroless/static-debian13

LABEL authors="WavyCat"
LABEL org.opencontainers.image.source="https://github.com/wavy-cat/compression-station"

WORKDIR /server
COPY --from=builder /src/server /server

ENTRYPOINT ["./main"]