SHELL := /bin/bash

# Muat .env bila ada, lalu ekspor ke semua perintah
-include .env
export

.DEFAULT_GOAL := help
.PHONY: help setup up down logs migrate seed db-reset grant verify keygen api worker web dev test test-backend lint-web build loadtest

help: ## Tampilkan daftar perintah
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS=":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

setup: ## Sekali di awal: buat .env, unduh dependensi Go & npm
	@test -f .env || (cp .env.example .env && echo "• .env dibuat dari .env.example")
	cd backend && go mod tidy
	cd frontend && npm install

up: ## Nyalakan Postgres, Redis, Mailpit (docker compose)
	docker compose up -d --wait

down: ## Matikan container infra
	docker compose down

logs: ## Lihat log container infra
	docker compose logs -f

migrate: ## Jalankan migrasi database
	cd backend && go run ./cmd/migrate up

seed: ## Isi data contoh (mitra, event, kategori, user dev)
	cd backend && go run ./cmd/migrate seed

db-reset: ## HAPUS semua tabel lalu migrasi + seed ulang
	cd backend && go run ./cmd/migrate reset && go run ./cmd/migrate up && go run ./cmd/migrate seed

grant: ## Beri peran mitra: make grant EMAIL=kamu@x.com ORG=jakarta-runners ROLE=owner
	cd backend && go run ./cmd/migrate grant "$(EMAIL)" "$(ORG)" "$(ROLE)"

verify: ## Verifikasi mitra agar bisa menerbitkan event: make verify ORG=slug
	cd backend && go run ./cmd/migrate verify "$(ORG)"

keygen: ## Buat kunci Ed25519 untuk QR tiket (tempel ke QR_PRIVATE_KEY)
	cd backend && go run ./cmd/keygen

api: ## Jalankan API Go (port 8080)
	cd backend && go run ./cmd/api

worker: ## Jalankan worker (tiket, ledger, pengembalian kuota)
	cd backend && go run ./cmd/worker

web: ## Jalankan frontend Next.js (port 3000)
	cd frontend && npm run dev

dev: ## Jalankan api + worker + web sekaligus
	$(MAKE) -j3 api worker web

test: test-backend lint-web ## Semua tes

test-backend: ## Tes unit backend
	cd backend && go vet ./... && go test ./...

lint-web: ## Lint + type-check frontend
	cd frontend && npm run lint && npx next typegen && npx tsc --noEmit

build: ## Build binary backend & frontend produksi
	cd backend && CGO_ENABLED=0 go build -o bin/ ./cmd/...
	cd frontend && npm run build

loadtest: ## Uji beban war tiket dengan k6 (butuh k6 terpasang)
	k6 run loadtest/war-tiket.js
