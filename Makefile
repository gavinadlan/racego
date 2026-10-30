SHELL := /bin/bash

# Muat .env bila ada, lalu ekspor ke semua perintah
-include .env
export

.DEFAULT_GOAL := help
.PHONY: help setup up down logs migrate seed db-reset grant verify superadmin dev-release keygen api worker web dev test test-backend lint-web build loadtest queue-demo queue-load \
	prod-build prod-up prod-down prod-logs prod-migrate prod-seed prod-grant prod-verify prod-superadmin backup restore demo demo-down

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

verify: ## Verifikasi mitra tanpa alur pengajuan (darurat/dev): make verify ORG=slug
	cd backend && go run ./cmd/migrate verify "$(ORG)"

superadmin: ## Jadikan akun super admin (/admin): make superadmin EMAIL=kamu@x.com (sudah pernah login)
	cd backend && go run ./cmd/migrate superadmin "$(EMAIL)"

dev-release: ## DEV: rilis semua saldo tertahan mitra tanpa masa tahan: make dev-release ORG=slug
	cd backend && go run ./cmd/migrate release "$(ORG)"

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

test-backend: ## Tes backend (butuh make up: tes Postgres memakai DATABASE_URL bila RACEGO_TEST_DATABASE_URL kosong)
	cd backend && go vet ./... && RACEGO_TEST_DATABASE_URL=$${RACEGO_TEST_DATABASE_URL:-$$DATABASE_URL} go test ./...

lint-web: ## Lint + type-check frontend
	cd frontend && npm run lint && npx next typegen && npx tsc --noEmit

build: ## Build binary backend & frontend produksi
	cd backend && CGO_ENABLED=0 go build -o bin/ ./cmd/...
	cd frontend && npm run build

loadtest: ## Uji beban war tiket dengan k6 (butuh k6 terpasang)
	k6 run loadtest/war-tiket.js

queue-demo: ## Event demo ber-antrian (kuota 50, buka 3 menit lagi); butuh make dev
	node scripts/queue-sim.mjs demo

USERS ?= 10000
QUOTA ?= 200
queue-load: ## Uji beban antrian virtual: make queue-load USERS=10000 QUOTA=200
	cd backend && go build -o bin/ ./cmd/api ./cmd/worker
	node scripts/queue-sim.mjs load --users $(USERS) --quota $(QUOTA) --spawn

# ===== Produksi (docker-compose.prod.yml + .env.prod), panduan: docs/10-deploy.md =====
# Variabel dari .env development (di-export di atas) dibuang dulu: compose mengutamakan environment
# shell di atas --env-file, jadi tanpa ini mis. AUTO_VERIFY_ORGS=true dari .env ikut ke produksi.
PROD_ENV ?= .env.prod
PROD_VARS := FRONTEND_URL $(shell sed -n 's/^\([A-Z_]*\)=.*/\1/p' .env.prod.example)
CLEAN_ENV := env $(addprefix -u ,$(PROD_VARS)) ENV_FILE=$(PROD_ENV)
PROD := $(CLEAN_ENV) docker compose -f docker-compose.prod.yml --env-file $(PROD_ENV)

prod-build: ## Build image backend & frontend produksi
	@$(PROD) build

prod-up: ## Build + jalankan stack produksi, tunggu semua sehat (juga untuk update)
	@test -f $(PROD_ENV) || (echo "$(PROD_ENV) belum ada: cp .env.prod.example .env.prod && scripts/gen-secrets.sh" && exit 1)
	@$(PROD) up -d --build --wait --remove-orphans
	@$(PROD) ps

prod-down: ## Matikan stack produksi (data di volume tetap ada)
	@$(PROD) down

prod-logs: ## Log stack produksi: make prod-logs [S=api]
	@$(PROD) logs -f --tail=200 $(S)

prod-migrate: ## Jalankan migrasi produksi manual (prod-up sudah otomatis)
	@$(PROD) run --rm migrate /app/migrate up

prod-seed: ## Isi data contoh di produksi (untuk demo; tanggal relatif hari ini)
	@$(PROD) run --rm migrate /app/migrate seed

prod-grant: ## Peran mitra produksi: make prod-grant EMAIL=kamu@x.com ORG=slug ROLE=owner
	@$(PROD) run --rm migrate /app/migrate grant "$(EMAIL)" "$(ORG)" "$(ROLE)"

prod-verify: ## Verifikasi mitra produksi tanpa alur pengajuan: make prod-verify ORG=slug
	@$(PROD) run --rm migrate /app/migrate verify "$(ORG)"

prod-superadmin: ## Super admin produksi: make prod-superadmin EMAIL=kamu@x.com (sudah pernah login)
	@$(PROD) run --rm migrate /app/migrate superadmin "$(EMAIL)"

backup: ## Backup database + unggahan ke backups/ (simpan 7 terakhir)
	@$(CLEAN_ENV) scripts/backup.sh

restore: ## Pulihkan backup: make restore FILE=backups/racego-YYYYMMDD-HHMMSS
	@$(CLEAN_ENV) scripts/restore.sh "$(FILE)"

demo: ## Demo dari laptop lewat Cloudflare quick tunnel (butuh cloudflared)
	@$(CLEAN_ENV) scripts/demo.sh

demo-down: ## Hentikan demo & tunnel
	@$(CLEAN_ENV) scripts/demo.sh down
