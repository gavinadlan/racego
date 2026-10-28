# Image tunggal berisi semua binary (api, worker, migrate, keygen, healthcheck).
# Build:  docker build -t racego-backend ./backend
# Jalan:  lihat docker-compose.prod.yml (api, worker, migrate memakai image ini)
# Versi Go mengikuti baris `go` di go.mod (minor yang sama, patch terbaru).
FROM golang:1.24-alpine AS build
WORKDIR /src
ENV GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/... \
 && mkdir -p /out/data/uploads

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/ /app/
# Folder volume unggahan dibuat dengan pemilik nonroot agar volume baru ikut bisa ditulis.
COPY --from=build --chown=nonroot:nonroot /out/data /data
EXPOSE 8080
USER nonroot
CMD ["/app/api"]
