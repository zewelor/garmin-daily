FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS build

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
