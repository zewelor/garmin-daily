FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/garmin-daily/ ./cmd/garmin-daily/
COPY garmin/ ./garmin/
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/garmin-daily ./cmd/garmin-daily

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/garmin-daily /garmin-daily
COPY LICENSE /LICENSE

USER 65532:65532
ENTRYPOINT ["/garmin-daily"]
