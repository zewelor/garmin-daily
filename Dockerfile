FROM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/garmin-daily/ ./cmd/garmin-daily/
COPY garmin/ ./garmin/
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/garmin-daily ./cmd/garmin-daily

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/garmin-daily /garmin-daily

USER 65532:65532
ENTRYPOINT ["/garmin-daily"]
