# Compila la API desde el código (para desarrollo). El instalador no usa este
# archivo: genera uno propio con el binario verificado de la release.
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /api-netbird-clients .

FROM scratch
COPY --from=build /api-netbird-clients /api-netbird-clients
ENV HOME=/tmp
HEALTHCHECK --interval=30s --timeout=20s --start-period=20s --retries=3 CMD ["/api-netbird-clients", "healthcheck"]
ENTRYPOINT ["/api-netbird-clients"]
CMD ["serve"]
