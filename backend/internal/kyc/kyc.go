// Package kyc adalah verifikasi mitra: owner mengirim identitas, dokumen, dan rekening bank;
// super admin menyetujui atau menolak. Lihat docs/02-arsitektur.md.
//
// Data sensitif (NIK PIC, nomor rekening, NPWP, isi dokumen) hanya disimpan terenkripsi
// (internal/secretbox) dan tidak pernah ditulis ke log, audit, atau email. Setiap akses ke
// dokumen dan setiap "tampilkan data lengkap" dicatat di audit_logs.
//
// organizations.kyc_status tetap sumber kebenaran penguncian penerbitan event:
// pengajuan approved → verified, rejected → rejected, selain itu pending.
package kyc

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/gavinadlan/racego/backend/internal/auth"
	"github.com/gavinadlan/racego/backend/internal/secretbox"
)

// ===== Matriks jenis penyelenggara & dokumen (satu-satunya sumber; dikirim ke frontend) =====

type OrgType string

const (
	TypePerorangan OrgType = "perorangan"
	TypeKomunitas  OrgType = "komunitas"
	TypeBadanUsaha OrgType = "badan_usaha"
)

type DocKind string

type TypeRule struct {
	Type        OrgType   `json:"type"`
	Label       string    `json:"label"`
	Description string    `json:"description"`
	Required    []DocKind `json:"required"`
	Optional    []DocKind `json:"optional"`
	HolderRule  string    `json:"holder_rule"` // aturan nama pemilik rekening
}

var Types = []TypeRule{
	{TypePerorangan, "Perorangan", "Penyelenggara atas nama pribadi.",
		[]DocKind{"ktp_pic"}, []DocKind{"npwp"},
		"Nama pemilik rekening sama dengan nama penanggung jawab."},
	{TypeKomunitas, "Komunitas", "Komunitas lari atau perkumpulan tanpa badan hukum.",
		[]DocKind{"ktp_pic", "surat_keterangan"}, []DocKind{"npwp"},
		"Nama pemilik rekening sama dengan nama penanggung jawab atau nama komunitas."},
	{TypeBadanUsaha, "Badan usaha", "PT, CV, atau yayasan.",
		[]DocKind{"ktp_pic", "npwp", "akta_pendirian", "sk_kemenkumham"}, []DocKind{"nib", "surat_kuasa"},
		"Nama pemilik rekening sama dengan nama badan usaha."},
}

var DocLabels = map[DocKind]string{
	"ktp_pic":          "KTP penanggung jawab",
	"npwp":             "NPWP",
	"surat_keterangan": "SK pengurus / surat pernyataan komunitas",
	"akta_pendirian":   "Akta pendirian",
	"sk_kemenkumham":   "SK Kemenkumham",
	"nib":              "NIB",
	"surat_kuasa":      "Surat kuasa",
}

func ruleFor(t OrgType) (TypeRule, bool) {
	for _, r := range Types {
		if r.Type == t {
			return r, true
		}
	}
	return TypeRule{}, false
}

// allowedKind: jenis dokumen yang dipakai untuk jenis penyelenggara t (wajib atau opsional).
func allowedKind(t OrgType, k DocKind) bool {
	r, ok := ruleFor(t)
	return ok && (slices.Contains(r.Required, k) || slices.Contains(r.Optional, k))
}

type Bank struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// BankOther: bank di luar daftar, nama diisi bebas.
const BankOther = "lainnya"

var Banks = []Bank{
	{"bca", "BCA"}, {"mandiri", "Bank Mandiri"}, {"bni", "BNI"}, {"bri", "BRI"}, {"bsi", "BSI"},
	{"cimb", "CIMB Niaga"}, {"permata", "Bank Permata"}, {"danamon", "Bank Danamon"},
	{"jago", "Bank Jago"}, {"seabank", "SeaBank"}, {"blu", "blu by BCA Digital"},
	{BankOther, "Lainnya"},
}

func bankName(code string) (string, bool) {
	for _, b := range Banks {
		if b.Code == code {
			return b.Name, true
		}
	}
	return "", false
}

// Teks persetujuan pemrosesan data. Ubah teks = naikkan versi (consent_version tersimpan per pengajuan).
const (
	ConsentVersion = "2026-10-v1"
	ConsentText    = "Saya setuju data dan dokumen ini dipakai RaceGo hanya untuk memverifikasi penyelenggara dan mencairkan dana penjualan tiket. " +
		"Data disimpan terenkripsi, hanya dilihat tim peninjau RaceGo, dan setiap aksesnya tercatat. " +
		"Data akan dihapus atau dianonimkan sesuai kebijakan retensi yang belum ditetapkan pada versi ini."
)

// ===== Handler =====

const (
	MaxDocBytes   = 5 << 20 // 5 MB per berkas
	maxDocsPerSub = 10
	uploadLimit   = 30 // unggahan per akun per jam
)

type Handler struct {
	db         *pgxpool.Pool
	rdb        redis.Cmdable
	box        *secretbox.Box
	files      Files
	autoVerify bool
	now        func() time.Time
}

func NewHandler(db *pgxpool.Pool, rdb redis.Cmdable, box *secretbox.Box, dir string, autoVerify bool) *Handler {
	return &Handler{db: db, rdb: rdb, box: box, files: Files{Dir: dir, Box: box}, autoVerify: autoVerify, now: time.Now}
}

type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// audit menulis audit_logs. after TIDAK BOLEH berisi nilai sensitif: hanya id, jenis dokumen, nama kolom, IP.
func audit(ctx context.Context, q querier, r *http.Request, orgID uuid.UUID, action, verificationID string, after map[string]any) error {
	actor, _ := auth.UserID(ctx)
	if after == nil {
		after = map[string]any{}
	}
	after["ip"] = auth.ClientIP(r)
	_, err := q.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, org_id, action, entity, entity_id, after)
		VALUES ($1, $2, $3, 'org_verification', $4, $5)`, actor, orgID, action, verificationID, after)
	return err
}

// queueEmail mengantrekan email status ke setiap owner org (satu pesan outbox per owner, dengan retry worker).
// Payload hanya id; isi email dibentuk worker dari nama org, status, dan alasan penolakan.
func queueEmail(ctx context.Context, q querier, verificationID uuid.UUID, event string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO outbox (topic, payload)
		SELECT 'kyc.email', jsonb_build_object('verification_id', v.id, 'event', $2::text, 'user_id', m.user_id)
		FROM org_verifications v JOIN org_members m ON m.org_id = v.org_id AND m.role = 'owner'
		WHERE v.id = $1`, verificationID, event)
	return err
}

var errNotFound = errors.New("tidak ditemukan")
