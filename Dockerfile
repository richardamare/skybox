# The control plane's image: skyboxctl alone on a distroless base, which carries the CA certificates the Azure and
# Cloudflare APIs need.
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /skyboxctl ./cmd/skyboxctl

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /skyboxctl /skyboxctl
ENTRYPOINT ["/skyboxctl"]
CMD ["reconcile"]
