# Image tunggal berisi semua binary (api, worker, migrate, keygen, healthcheck).
# Build:  docker build -t racego-backend ./backend
# Jalan:  lihat docker-compose.prod.yml (api, worker, migrate memakai image ini)
# Versi Go sama dengan baris `toolchain` di go.mod (CI memakai go.mod yang sama lewat setup-go).
FROM golang:1.27.3-alpine AS build
WORKDIR /src
ENV GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/... \
 && mkdir -p /out/data/uploads /out/data/kyc

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/ /app/
# Folder volume unggahan & dokumen KYC dibuat dengan pemilik nonroot agar volume baru ikut bisa ditulis.
COPY --from=build --chown=nonroot:nonroot /out/data /data
EXPOSE 8080
USER nonroot
CMD ["/app/api"]
