# Image tunggal berisi semua binary (api, worker, migrate, keygen).
# Build:  docker build -t racego-backend ./backend
# Jalan:  docker run --env-file .env racego-backend /app/api
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/...

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/ /app/
EXPOSE 8080
USER nonroot
CMD ["/app/api"]
