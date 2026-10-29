package kyc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gavinadlan/racego/backend/internal/auth"
	"github.com/gavinadlan/racego/backend/internal/httpx"
	"github.com/gavinadlan/racego/backend/internal/ratelimit"
	"github.com/gavinadlan/racego/backend/internal/secretbox"
	"github.com/gavinadlan/racego/backend/internal/tenant"
)

// apiError: penolakan aturan bisnis yang dikirim apa adanya ke klien.
type apiError struct {
	status    int
	code, msg string
	fields    httpx.Fields
}

func (e apiError) Error() string { return e.msg }

var (
	errAlreadyVerified = apiError{status: http.StatusConflict, code: "already_verified", msg: "Penyelenggara ini sudah terverifikasi."}
	errNotEditable     = apiError{status: http.StatusConflict, code: "not_editable", msg: "Pengajuan sedang ditinjau dan tidak bisa diubah lagi."}
	errReopenRequired  = apiError{status: http.StatusConflict, code: "reopen_required", msg: "Pengajuan ditolak. Klik Ajukan ulang untuk memperbaiki dan mengirim lagi."}
)

func validationErr(f httpx.Fields) apiError {
	return apiError{status: http.StatusUnprocessableEntity, code: "validation", fields: f}
}

func writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ae apiError
	var de DocError
	switch {
	case errors.As(err, &ae) && ae.code == "validation":
		httpx.Validation(w, ae.fields)
	case errors.As(err, &ae):
		httpx.Error(w, ae.status, ae.code, ae.msg)
	case errors.As(err, &de):
		status := http.StatusUnprocessableEntity
		if de.Code == "file_too_large" {
			status = http.StatusRequestEntityTooLarge
		}
		httpx.Error(w, status, de.Code, de.Message)
	case errors.Is(err, errNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "Data tidak ditemukan")
	case errors.Is(err, secretbox.ErrDecrypt), errors.Is(err, secretbox.ErrUnknownVersion):
		slog.ErrorContext(r.Context(), "dekripsi data KYC gagal", "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "decrypt_failed",
			"Data tidak bisa dibuka: kunci enkripsi server tidak cocok atau berkas rusak. Hubungi tim RaceGo.")
	default:
		httpx.Internal(w, r, err)
	}
}

// ===== Data pengajuan =====

type verification struct {
	ID, OrgID                                uuid.UUID
	Attempt                                  int
	Status                                   string
	OrgType                                  *string
	LegalName, Address, PICName, PICPhone    string
	PICIDEnc, PICIDLast4, NPWPEnc, NPWPLast4 *string
	BankCode, BankName, AccountHolder        string
	AccountEnc, AccountLast4                 *string
	ConsentAt                                *time.Time
	ConsentVersion                           *string
	SubmittedAt, ReviewedAt                  *time.Time
	ReviewedBy                               *uuid.UUID
	RejectReason                             *string
	CreatedAt, UpdatedAt                     time.Time
}

const vCols = `id, org_id, attempt, status, org_type, legal_name, address, pic_name, pic_phone,
	pic_id_enc, pic_id_last4, npwp_enc, npwp_last4, bank_code, bank_name, account_holder, account_enc, account_last4,
	consent_at, consent_version, submitted_at, reviewed_at, reviewed_by, reject_reason, created_at, updated_at`

func scanV(row pgx.Row) (*verification, error) {
	var v verification
	err := row.Scan(&v.ID, &v.OrgID, &v.Attempt, &v.Status, &v.OrgType, &v.LegalName, &v.Address, &v.PICName, &v.PICPhone,
		&v.PICIDEnc, &v.PICIDLast4, &v.NPWPEnc, &v.NPWPLast4, &v.BankCode, &v.BankName, &v.AccountHolder, &v.AccountEnc, &v.AccountLast4,
		&v.ConsentAt, &v.ConsentVersion, &v.SubmittedAt, &v.ReviewedAt, &v.ReviewedBy, &v.RejectReason, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	return &v, err
}

// latest: pengajuan terbaru org (nil bila belum pernah ada).
func latest(ctx context.Context, q querier, orgID uuid.UUID) (*verification, error) {
	v, err := scanV(q.QueryRow(ctx, `SELECT `+vCols+` FROM org_verifications WHERE org_id = $1 ORDER BY attempt DESC LIMIT 1`, orgID))
	if errors.Is(err, errNotFound) {
		return nil, nil
	}
	return v, err
}

func (v *verification) orgType() OrgType {
	if v.OrgType == nil {
		return ""
	}
	return OrgType(*v.OrgType)
}

type doc struct {
	Kind        DocKind   `json:"kind"`
	Label       string    `json:"label"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	Size        int       `json:"size"`
	CreatedAt   time.Time `json:"created_at"`
	storageKey  string
	sha256      string
}

func loadDocs(ctx context.Context, q querier, verificationID uuid.UUID) ([]doc, error) {
	rows, err := q.Query(ctx, `
		SELECT kind, original_name, content_type, size, created_at, storage_key, sha256
		FROM org_documents WHERE verification_id = $1 ORDER BY created_at`, verificationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []doc{}
	for rows.Next() {
		var d doc
		if err := rows.Scan(&d.Kind, &d.Name, &d.ContentType, &d.Size, &d.CreatedAt, &d.storageKey, &d.sha256); err != nil {
			return nil, err
		}
		d.Label = DocLabels[d.Kind]
		out = append(out, d)
	}
	return out, rows.Err()
}

func mask(last4 *string) string {
	if last4 == nil {
		return ""
	}
	return "••••" + *last4
}

// View: pengajuan dengan data sensitif tersamar (••••1234).
type View struct {
	ID             uuid.UUID  `json:"id"`
	Attempt        int        `json:"attempt"`
	Status         string     `json:"status"`
	OrgType        string     `json:"org_type"`
	LegalName      string     `json:"legal_name"`
	Address        string     `json:"address"`
	PICName        string     `json:"pic_name"`
	PICPhone       string     `json:"pic_phone"`
	PICID          string     `json:"pic_id"`
	NPWP           string     `json:"npwp"`
	BankCode       string     `json:"bank_code"`
	BankName       string     `json:"bank_name"`
	Account        string     `json:"account"`
	AccountHolder  string     `json:"account_holder"`
	ConsentAt      *time.Time `json:"consent_at"`
	ConsentVersion *string    `json:"consent_version"`
	SubmittedAt    *time.Time `json:"submitted_at"`
	ReviewedAt     *time.Time `json:"reviewed_at"`
	RejectReason   *string    `json:"reject_reason"`
	CreatedAt      time.Time  `json:"created_at"`
	Documents      []doc      `json:"documents"`
}

func (v *verification) view(docs []doc) View {
	return View{ID: v.ID, Attempt: v.Attempt, Status: v.Status, OrgType: string(v.orgType()), LegalName: v.LegalName, Address: v.Address,
		PICName: v.PICName, PICPhone: v.PICPhone, PICID: mask(v.PICIDLast4), NPWP: mask(v.NPWPLast4), BankCode: v.BankCode,
		BankName: v.BankName, Account: mask(v.AccountLast4), AccountHolder: v.AccountHolder, ConsentAt: v.ConsentAt,
		ConsentVersion: v.ConsentVersion, SubmittedAt: v.SubmittedAt, ReviewedAt: v.ReviewedAt, RejectReason: v.RejectReason,
		CreatedAt: v.CreatedAt, Documents: docs}
}

type HistoryItem struct {
	ID           uuid.UUID  `json:"id"`
	Attempt      int        `json:"attempt"`
	Status       string     `json:"status"`
	SubmittedAt  *time.Time `json:"submitted_at"`
	ReviewedAt   *time.Time `json:"reviewed_at"`
	RejectReason *string    `json:"reject_reason"`
}

func history(ctx context.Context, q querier, orgID uuid.UUID) ([]HistoryItem, error) {
	rows, err := q.Query(ctx, `
		SELECT id, attempt, status, submitted_at, reviewed_at, reject_reason
		FROM org_verifications WHERE org_id = $1 ORDER BY attempt DESC`, orgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[HistoryItem])
}

// Requirements: matriks dokumen, daftar bank, teks persetujuan; dikirim ke frontend agar tidak diduplikasi.
type Requirements struct {
	Types     []TypeRule         `json:"types"`
	Documents map[DocKind]string `json:"documents"`
	Banks     []Bank             `json:"banks"`
	Consent   struct {
		Version string `json:"version"`
		Text    string `json:"text"`
	} `json:"consent"`
	MaxBytes int `json:"max_bytes"`
}

func requirements() Requirements {
	r := Requirements{Types: Types, Documents: DocLabels, Banks: Banks, MaxBytes: MaxDocBytes}
	r.Consent.Version, r.Consent.Text = ConsentVersion, ConsentText
	return r
}

type State struct {
	KYCStatus    string        `json:"kyc_status"`
	AutoVerify   bool          `json:"auto_verify"`
	Verification *View         `json:"verification"`
	History      []HistoryItem `json:"history"`
	Requirements Requirements  `json:"requirements"`
}

func (h *Handler) state(ctx context.Context, q querier, orgID uuid.UUID) (State, error) {
	s := State{AutoVerify: h.autoVerify, Requirements: requirements()}
	if err := q.QueryRow(ctx, `SELECT kyc_status FROM organizations WHERE id = $1`, orgID).Scan(&s.KYCStatus); err != nil {
		return s, err
	}
	v, err := latest(ctx, q, orgID)
	if err != nil || v == nil {
		s.History = []HistoryItem{}
		return s, err
	}
	docs, err := loadDocs(ctx, q, v.ID)
	if err != nil {
		return s, err
	}
	view := v.view(docs)
	s.Verification = &view
	s.History, err = history(ctx, q, orgID)
	return s, err
}

// draftForEdit mengunci org lalu mengembalikan draft yang boleh diubah (dibuat bila belum ada).
func draftForEdit(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) (*verification, error) {
	var kyc string
	if err := tx.QueryRow(ctx, `SELECT kyc_status FROM organizations WHERE id = $1 FOR UPDATE`, orgID).Scan(&kyc); err != nil {
		return nil, err
	}
	if kyc == "verified" {
		return nil, errAlreadyVerified
	}
	v, err := latest(ctx, tx, orgID)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return scanV(tx.QueryRow(ctx, `INSERT INTO org_verifications (org_id, attempt) VALUES ($1, 1) RETURNING `+vCols, orgID))
	}
	switch v.Status {
	case "draft":
		return v, nil
	case "submitted":
		return nil, errNotEditable
	case "rejected":
		return nil, errReopenRequired
	}
	return nil, errAlreadyVerified
}

// ===== Endpoint owner: /v1/org/{org}/verification =====

// Get: GET /verification — pengajuan terkini (tersamar), matriks dokumen, riwayat.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	s, err := h.state(r.Context(), h.db, tenant.OrgID(r.Context()))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

// Input isian draft. pic_id/npwp/account_number: null = tidak diubah (nilai lama tidak pernah dikirim
// balik ke browser), "" = dihapus.
type Input struct {
	OrgType       string  `json:"org_type"`
	LegalName     string  `json:"legal_name"`
	Address       string  `json:"address"`
	PICName       string  `json:"pic_name"`
	PICPhone      string  `json:"pic_phone"`
	PICID         *string `json:"pic_id"`
	NPWP          *string `json:"npwp"`
	BankCode      string  `json:"bank_code"`
	BankName      string  `json:"bank_name"`
	AccountNumber *string `json:"account_number"`
	AccountHolder string  `json:"account_holder"`
}

var (
	nikRe     = regexp.MustCompile(`^[0-9]{16}$`)
	npwpRe    = regexp.MustCompile(`^[0-9]{15,16}$`)
	accountRe = regexp.MustCompile(`^[0-9]{6,20}$`)
)

func digitsOnly(s string) string {
	return strings.NewReplacer(" ", "", ".", "", "-", "").Replace(strings.TrimSpace(s))
}

func checkLen(f httpx.Fields, key, v string, max int) {
	if utf8.RuneCountInString(v) > max {
		f[key] = fmt.Sprintf("Maksimal %d karakter", max)
	}
}

// normalize merapikan & memvalidasi FORMAT isian yang terisi. Kelengkapan dicek saat dikirim.
func (in *Input) normalize() httpx.Fields {
	f := httpx.Fields{}
	for _, p := range []*string{&in.OrgType, &in.LegalName, &in.Address, &in.PICName, &in.PICPhone, &in.BankCode, &in.BankName, &in.AccountHolder} {
		*p = strings.Join(strings.Fields(*p), " ")
	}
	if _, ok := ruleFor(OrgType(in.OrgType)); in.OrgType != "" && !ok {
		f["org_type"] = "Jenis penyelenggara tidak dikenal"
	}
	checkLen(f, "legal_name", in.LegalName, 150)
	checkLen(f, "address", in.Address, 500)
	checkLen(f, "pic_name", in.PICName, 100)
	checkLen(f, "account_holder", in.AccountHolder, 100)
	if in.PICPhone != "" {
		if p, ok := auth.NormalizePhone(in.PICPhone); ok {
			in.PICPhone = p
		} else {
			f["pic_phone"] = "Nomor HP harus nomor Indonesia, mis. 081234567890"
		}
	}
	for _, c := range []struct {
		key string
		v   *string
		re  *regexp.Regexp
		msg string
	}{
		{"pic_id", in.PICID, nikRe, "NIK harus 16 digit angka"},
		{"npwp", in.NPWP, npwpRe, "Nomor NPWP harus 15 atau 16 digit angka"},
		{"account_number", in.AccountNumber, accountRe, "Nomor rekening harus 6–20 digit angka"},
	} {
		if c.v == nil {
			continue
		}
		*c.v = digitsOnly(*c.v)
		if *c.v != "" && !c.re.MatchString(*c.v) {
			f[c.key] = c.msg
		}
	}
	if in.BankCode != "" {
		name, ok := bankName(in.BankCode)
		switch {
		case !ok:
			f["bank_code"] = "Bank tidak dikenal"
		case in.BankCode != BankOther:
			in.BankName = name
		default:
			checkLen(f, "bank_name", in.BankName, 60)
		}
	} else {
		in.BankName = ""
	}
	return f
}

// sealed mengembalikan (ciphertext, 4 digit terakhir) untuk kolom sensitif; keduanya nil bila kosong.
func (h *Handler) sealed(v string) (*string, *string) {
	if v == "" {
		return nil, nil
	}
	enc, last4 := h.box.SealString(v), v[len(v)-4:]
	return &enc, &last4
}

// Save: PUT /verification — simpan isian draft.
func (h *Handler) Save(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in Input
	if !httpx.Decode(w, r, &in) {
		return
	}
	if f := in.normalize(); len(f) > 0 {
		httpx.Validation(w, f)
		return
	}
	orgID := tenant.OrgID(ctx)
	var orphans []string
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		v, err := draftForEdit(ctx, tx, orgID)
		if err != nil {
			return err
		}
		var orgType *string
		if in.OrgType != "" {
			orgType = &in.OrgType
		}
		if _, err := tx.Exec(ctx, `
			UPDATE org_verifications SET org_type = $2, legal_name = $3, address = $4, pic_name = $5, pic_phone = $6,
			       bank_code = $7, bank_name = $8, account_holder = $9, updated_at = now()
			WHERE id = $1`, v.ID, orgType, in.LegalName, in.Address, in.PICName, in.PICPhone, in.BankCode, in.BankName, in.AccountHolder); err != nil {
			return err
		}
		for _, s := range []struct {
			col string
			v   *string
		}{{"pic_id", in.PICID}, {"npwp", in.NPWP}, {"account", in.AccountNumber}} {
			if s.v == nil {
				continue
			}
			enc, last4 := h.sealed(*s.v)
			if _, err := tx.Exec(ctx, `UPDATE org_verifications SET `+s.col+`_enc = $2, `+s.col+`_last4 = $3 WHERE id = $1`, v.ID, enc, last4); err != nil {
				return err
			}
		}
		// Ganti jenis: dokumen yang tidak dipakai jenis baru dibuang.
		if in.OrgType != string(v.orgType()) {
			docs, err := loadDocs(ctx, tx, v.ID)
			if err != nil {
				return err
			}
			for _, d := range docs {
				if allowedKind(OrgType(in.OrgType), d.Kind) {
					continue
				}
				if _, err := tx.Exec(ctx, `DELETE FROM org_documents WHERE verification_id = $1 AND kind = $2`, v.ID, d.Kind); err != nil {
					return err
				}
				orphans = append(orphans, d.storageKey)
			}
		}
		return nil
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	h.removeOrphans(ctx, orphans)
	h.Get(w, r)
}

// removeOrphans menghapus berkas yang tidak lagi dirujuk baris dokumen mana pun (dipanggil setelah commit).
func (h *Handler) removeOrphans(ctx context.Context, keys []string) {
	for _, k := range keys {
		var used bool
		if err := h.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM org_documents WHERE storage_key = $1)`, k).Scan(&used); err != nil {
			slog.ErrorContext(ctx, "cek rujukan berkas KYC", "key", k, "err", err)
			continue
		}
		if !used {
			if err := h.files.Delete(k); err != nil {
				slog.ErrorContext(ctx, "hapus berkas KYC", "key", k, "err", err)
			}
		}
	}
}

// Upload: POST /verification/documents (multipart: kind, file).
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, _ := auth.UserID(ctx)
	if err := ratelimit.Allow(ctx, h.rdb, "kyc:upload:"+userID.String(), uploadLimit, time.Hour); err != nil {
		var ex ratelimit.Exceeded
		if errors.As(err, &ex) {
			secs := int(math.Ceil(ex.RetryAfter.Seconds()))
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			httpx.JSON(w, http.StatusTooManyRequests, map[string]any{"error": "too_many_requests",
				"message": fmt.Sprintf("Terlalu banyak unggahan. Coba lagi dalam %d menit.", (secs+59)/60), "retry_after": secs})
			return
		}
		httpx.Internal(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxDocBytes+64<<10)
	f, hdr, err := r.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeErr(w, r, errDocTooLarge)
			return
		}
		httpx.Validation(w, httpx.Fields{"file": "Pilih berkas untuk diunggah"})
		return
	}
	defer f.Close()
	kind := DocKind(r.FormValue("kind"))
	if _, ok := DocLabels[kind]; !ok {
		httpx.Validation(w, httpx.Fields{"kind": "Jenis dokumen tidak dikenal"})
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxDocBytes+1))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	contentType, clean, err := ValidateDocument(data)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	key, err := h.files.Put(clean)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	orgID := tenant.OrgID(ctx)
	var orphans []string
	err = pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		v, err := draftForEdit(ctx, tx, orgID)
		if err != nil {
			return err
		}
		t := v.orgType()
		if t == "" {
			return validationErr(httpx.Fields{"kind": "Pilih jenis penyelenggara dulu (langkah 1)."})
		}
		if !allowedKind(t, kind) {
			r, _ := ruleFor(t)
			return validationErr(httpx.Fields{"kind": DocLabels[kind] + " tidak dipakai untuk jenis " + r.Label + "."})
		}
		docs, err := loadDocs(ctx, tx, v.ID)
		if err != nil {
			return err
		}
		replacing := false
		for _, d := range docs {
			if d.Kind == kind {
				replacing = true
				orphans = append(orphans, d.storageKey)
			}
		}
		if !replacing && len(docs) >= maxDocsPerSub {
			return validationErr(httpx.Fields{"kind": fmt.Sprintf("Maksimal %d berkas per pengajuan", maxDocsPerSub)})
		}
		sum := sha256Hex(clean)
		if _, err := tx.Exec(ctx, `
			INSERT INTO org_documents (verification_id, kind, storage_key, original_name, content_type, size, sha256)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (verification_id, kind) DO UPDATE SET storage_key = EXCLUDED.storage_key,
			  original_name = EXCLUDED.original_name, content_type = EXCLUDED.content_type, size = EXCLUDED.size,
			  sha256 = EXCLUDED.sha256, created_at = now()`,
			v.ID, kind, key, SafeName(hdr.Filename, contentType), contentType, len(clean), sum); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE org_verifications SET updated_at = now() WHERE id = $1`, v.ID); err != nil {
			return err
		}
		return audit(ctx, tx, r, orgID, "kyc.document.upload", v.ID.String(), map[string]any{"kind": kind, "content_type": contentType, "size": len(clean)})
	})
	if err != nil {
		h.removeOrphans(ctx, []string{key})
		writeErr(w, r, err)
		return
	}
	h.removeOrphans(ctx, orphans)
	h.Get(w, r)
}

// DeleteDocument: DELETE /verification/documents/{kind}.
func (h *Handler) DeleteDocument(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgID := tenant.OrgID(ctx)
	var key string
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		v, err := draftForEdit(ctx, tx, orgID)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `DELETE FROM org_documents WHERE verification_id = $1 AND kind = $2 RETURNING storage_key`,
			v.ID, chi.URLParam(r, "kind")).Scan(&key)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		return err
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	h.removeOrphans(ctx, []string{key})
	h.Get(w, r)
}

// Document: GET /verification/documents/{kind} — berkas sendiri dari pengajuan terkini (tercatat).
func (h *Handler) Document(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgID := tenant.OrgID(ctx)
	v, err := latest(ctx, h.db, orgID)
	if err == nil && v == nil {
		err = errNotFound
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	h.serveDocument(w, r, v, DocKind(chi.URLParam(r, "kind")), "owner")
}

// serveDocument mendekripsi dan mengirim berkas dengan header ketat. Akses dicatat SEBELUM berkas dikirim.
func (h *Handler) serveDocument(w http.ResponseWriter, r *http.Request, v *verification, kind DocKind, by string) {
	ctx := r.Context()
	var d doc
	err := h.db.QueryRow(ctx, `SELECT original_name, content_type, storage_key FROM org_documents WHERE verification_id = $1 AND kind = $2`,
		v.ID, kind).Scan(&d.Name, &d.ContentType, &d.storageKey)
	if errors.Is(err, pgx.ErrNoRows) {
		err = errNotFound
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	data, err := h.files.Read(d.storageKey)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := audit(ctx, h.db, r, v.OrgID, "kyc.document.view", v.ID.String(), map[string]any{"kind": kind, "by": by}); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", d.ContentType)
	hd.Set("Content-Length", strconv.Itoa(len(data)))
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Cache-Control", "private, no-store")
	hd.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	hd.Set("Content-Disposition", "inline; filename*=UTF-8''"+url.PathEscape(SafeName(d.Name, d.ContentType)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// Submit: POST /verification/submit {"consent": true} — cek kelengkapan lalu kirim untuk ditinjau.
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Consent bool `json:"consent"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	orgID := tenant.OrgID(ctx)
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		v, err := draftForEdit(ctx, tx, orgID)
		if err != nil {
			return err
		}
		docs, err := loadDocs(ctx, tx, v.ID)
		if err != nil {
			return err
		}
		if f := v.missing(docs, in.Consent); len(f) > 0 {
			return validationErr(f)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE org_verifications SET status = 'submitted', consent_at = now(), consent_version = $2, submitted_at = now(), updated_at = now()
			WHERE id = $1`, v.ID, ConsentVersion); err != nil {
			return err
		}
		if err := audit(ctx, tx, r, orgID, "kyc.submitted", v.ID.String(), map[string]any{"attempt": v.Attempt, "documents": len(docs)}); err != nil {
			return err
		}
		return queueEmail(ctx, tx, v.ID, "submitted")
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	h.Get(w, r)
}

// missing: kolom yang belum lengkap untuk dikirim (pesan per kolom).
func (v *verification) missing(docs []doc, consent bool) httpx.Fields {
	f := httpx.Fields{}
	req := func(key string, ok bool, msg string) {
		if !ok {
			f[key] = msg
		}
	}
	t := v.orgType()
	rule, typed := ruleFor(t)
	req("org_type", typed, "Pilih jenis penyelenggara")
	req("legal_name", v.LegalName != "", "Wajib diisi sesuai dokumen")
	req("address", utf8.RuneCountInString(v.Address) >= 10, "Isi alamat lengkap (minimal 10 karakter)")
	req("pic_name", v.PICName != "", "Wajib diisi")
	req("pic_phone", v.PICPhone != "", "Wajib diisi")
	req("pic_id", v.PICIDEnc != nil, "NIK penanggung jawab wajib diisi")
	if t == TypeBadanUsaha {
		req("npwp", v.NPWPEnc != nil, "Nomor NPWP badan usaha wajib diisi")
	}
	req("bank_code", v.BankCode != "", "Pilih bank")
	if v.BankCode == BankOther {
		req("bank_name", v.BankName != "", "Tulis nama bank")
	}
	req("account_number", v.AccountEnc != nil, "Nomor rekening wajib diisi")
	req("account_holder", v.AccountHolder != "", "Nama pemilik rekening wajib diisi")
	have := map[DocKind]bool{}
	for _, d := range docs {
		have[d.Kind] = true
	}
	for _, k := range rule.Required {
		req("documents."+string(k), have[k], DocLabels[k]+" wajib diunggah")
	}
	req("consent", consent, "Centang persetujuan pemrosesan data untuk mengirim")
	return f
}

// Reopen: POST /verification/reopen — hanya setelah ditolak: pengajuan baru (attempt+1) berisi salinan isian & dokumen.
func (h *Handler) Reopen(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgID := tenant.OrgID(ctx)
	err := pgx.BeginFunc(ctx, h.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM organizations WHERE id = $1 FOR UPDATE`, orgID); err != nil {
			return err
		}
		v, err := latest(ctx, tx, orgID)
		if err != nil {
			return err
		}
		if v == nil || v.Status != "rejected" {
			return apiError{status: http.StatusConflict, code: "invalid_status", msg: "Hanya pengajuan yang ditolak yang bisa diajukan ulang."}
		}
		var newID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO org_verifications (org_id, attempt, org_type, legal_name, address, pic_name, pic_phone,
			  pic_id_enc, pic_id_last4, npwp_enc, npwp_last4, bank_code, bank_name, account_holder, account_enc, account_last4)
			SELECT org_id, attempt + 1, org_type, legal_name, address, pic_name, pic_phone,
			  pic_id_enc, pic_id_last4, npwp_enc, npwp_last4, bank_code, bank_name, account_holder, account_enc, account_last4
			FROM org_verifications WHERE id = $1 RETURNING id`, v.ID).Scan(&newID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO org_documents (verification_id, kind, storage_key, original_name, content_type, size, sha256, created_at)
			SELECT $2, kind, storage_key, original_name, content_type, size, sha256, created_at
			FROM org_documents WHERE verification_id = $1`, v.ID, newID); err != nil {
			return err
		}
		return audit(ctx, tx, r, orgID, "kyc.reopened", newID.String(), map[string]any{"attempt": v.Attempt + 1, "from": v.ID})
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	h.Get(w, r)
}
