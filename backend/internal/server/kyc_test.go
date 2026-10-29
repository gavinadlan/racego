package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/gavinadlan/racego/backend/internal/config"
	"github.com/gavinadlan/racego/backend/internal/notify"
	"github.com/gavinadlan/racego/backend/internal/payment"
	"github.com/gavinadlan/racego/backend/internal/secretbox"
	"github.com/gavinadlan/racego/backend/internal/ticket"
)

// Uji verifikasi mitra (KYC) lewat router lengkap: Postgres sungguhan, Redis miniredis,
// dokumen terenkripsi di folder sementara.

// Nilai uji (bukan data asli). Dicari sebagai teks di DB, berkas, audit, outbox, dan log.
const (
	testNIK     = "3201987654320123"
	testNPWP    = "019876543290000"
	testAccount = "8877665544"
)

type kycEnv struct {
	*portalEnv
	dir        string
	autoVerify bool
}

func newKYCEnv(t *testing.T, autoVerify bool) *kycEnv {
	t.Helper()
	pe := newPortalEnv(t, autoVerify)
	key := make([]byte, 32)
	rand.Read(key) //nolint:errcheck
	box, _ := secretbox.New(key)
	e := &kycEnv{portalEnv: pe, dir: t.TempDir(), autoVerify: autoVerify}
	e.h = e.router(box)
	// Didaftarkan setelah newPortalEnv → jalan lebih dulu (LIFO), sebelum org & user dihapus.
	t.Cleanup(func() {
		pe.db.Exec(context.Background(), `DELETE FROM outbox WHERE topic = 'kyc.email' AND (payload->>'verification_id')::uuid IN (
			SELECT v.id FROM org_verifications v JOIN organizations o ON o.id = v.org_id
			WHERE o.created_by IN (SELECT id FROM users WHERE email LIKE $1))`, "%"+pe.tag+"@uji.test") //nolint:errcheck
	})
	return e
}

func (e *kycEnv) router(box *secretbox.Box) http.Handler {
	signer, _ := ticket.NewSigner("", true)
	rdb := redis.NewClient(&redis.Options{Addr: e.mr.Addr()})
	e.t.Cleanup(func() { rdb.Close() })
	cfg := config.Config{Env: "development", FrontendURL: "http://localhost:3000", ReservationTTL: 15 * time.Minute,
		AutoVerifyOrgs: e.autoVerify, UploadDir: e.t.TempDir(), KYCDir: e.dir,
		Queue: config.QueueConfig{LobbyMinutes: 15, AdmitPerSec: 20, MaxActive: 300, PassTTL: 10 * time.Minute, HeartbeatTTL: 45 * time.Second}}
	return NewRouter(Deps{Cfg: cfg, DB: e.db, Redis: rdb, Inv: e.inv, Snap: payment.NewMockSnap(cfg.FrontendURL), Signer: signer,
		Mailer: notify.NewMailer("localhost:1", "", "", "x@x"), Queue: e.q, KYCBox: box})
}

func (e *kycEnv) superAdmin(name string) uuid.UUID {
	id := e.user(name)
	if _, err := e.db.Exec(context.Background(), `UPDATE users SET is_super_admin = true WHERE id = $1`, id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *kycEnv) uploadDoc(user uuid.UUID, org, kind, filename string, data []byte) resp {
	e.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("kind", kind) //nolint:errcheck
	fw, _ := mw.CreateFormFile("file", filename)
	fw.Write(data) //nolint:errcheck
	mw.Close()
	req := httptest.NewRequest("POST", "/v1/org/"+org+"/verification/documents", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-User-ID", user.String())
	return e.serve(req)
}

func (e *kycEnv) serve(req *http.Request) resp {
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	out := resp{code: rec.Code, raw: rec.Body.String()}
	json.Unmarshal(rec.Body.Bytes(), &out.body) //nolint:errcheck
	return out
}

// get mengambil respons mentah (untuk unduhan dokumen).
func (e *kycEnv) get(user uuid.UUID, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	if user != uuid.Nil {
		req.Header.Set("X-User-ID", user.String())
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func samplePDF(text string) []byte {
	var b bytes.Buffer
	var offs []int
	obj := func(s string) {
		offs = append(offs, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", len(offs), s)
	}
	b.WriteString("%PDF-1.4\n")
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj("<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	obj("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")
	stream := "BT /F1 16 Tf 20 100 Td (" + text + ") Tj ET"
	obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	return b.Bytes()
}

func sampleJPEG(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 16, 10))
	img.Set(2, 2, color.RGBA{200, 10, 10, 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	// Sisipkan EXIF berisi penanda GPS: harus hilang setelah disimpan.
	payload := []byte("Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08GPSLatitude-6.2")
	seg := append([]byte{0xFF, 0xE1, 0, byte(len(payload) + 2)}, payload...)
	d := buf.Bytes()
	return append(append(append([]byte{}, d[:2]...), seg...), d[2:]...)
}

func badanUsahaInput() map[string]any {
	return map[string]any{
		"org_type": "badan_usaha", "legal_name": "PT Lari Sehat Uji", "address": "Jl. Contoh No. 1, Bogor, Jawa Barat",
		"pic_name": "Budi Contoh", "pic_phone": "081234567890", "pic_id": testNIK, "npwp": testNPWP,
		"bank_code": "bca", "account_number": testAccount, "account_holder": "PT. Lari Sehat Uji",
	}
}

func (e *kycEnv) countAudit(orgSlug, action string) int {
	var n int
	e.db.QueryRow(context.Background(), `SELECT count(*) FROM audit_logs a JOIN organizations o ON o.id = a.org_id
		WHERE o.slug = $1 AND a.action = $2`, orgSlug, action).Scan(&n) //nolint:errcheck
	return n
}

func TestKYCFullFlow(t *testing.T) {
	e := newKYCEnv(t, false)
	ctx := context.Background()
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	owner := e.user("owner")
	slug := e.createOrg(owner, "Org KYC "+e.tag)
	base := "/v1/org/" + slug + "/verification"

	st := e.must(e.do(owner, "GET", base, nil), 200)
	if st.body["verification"] != nil || st.body["kyc_status"] != "pending" {
		t.Fatalf("awal: %s", st.raw)
	}
	if types := st.body["requirements"].(map[string]any)["types"].([]any); len(types) != 3 {
		t.Fatalf("matriks: %v", types)
	}

	// Validasi format per kolom.
	r := e.do(owner, "PUT", base, map[string]any{"pic_id": "123", "npwp": "12", "account_number": "12ab", "pic_phone": "999", "org_type": "pt"})
	for _, f := range []string{"pic_id", "npwp", "account_number", "pic_phone", "org_type"} {
		if r.code != 422 || r.field(f) == "" {
			t.Fatalf("validasi %s: %s", f, r.raw)
		}
	}
	// Unggah sebelum memilih jenis ditolak.
	if r := e.uploadDoc(owner, slug, "ktp_pic", "ktp.jpg", sampleJPEG(t)); r.code != 422 || r.field("kind") == "" {
		t.Fatalf("unggah tanpa jenis: %s", r.raw)
	}
	st = e.must(e.do(owner, "PUT", base, badanUsahaInput()), 200)
	v := st.body["verification"].(map[string]any)
	if v["pic_id"] != "••••0123" || v["account"] != "••••5544" || v["npwp"] != "••••0000" || strings.Contains(st.raw, testNIK) {
		t.Fatalf("data harus tersamar: %s", st.raw)
	}

	// Kirim tanpa dokumen & persetujuan: ditolak per kolom.
	r = e.do(owner, "POST", base+"/submit", map[string]any{"consent": false})
	for _, f := range []string{"documents.ktp_pic", "documents.npwp", "documents.akta_pendirian", "documents.sk_kemenkumham", "consent"} {
		if r.code != 422 || r.field(f) == "" {
			t.Fatalf("submit tidak lengkap (%s): %s", f, r.raw)
		}
	}
	if r.field("documents.nib") != "" {
		t.Fatalf("NIB opsional: %s", r.raw)
	}
	// Rekening kosong juga ditolak.
	e.must(e.do(owner, "PUT", base, map[string]any{"org_type": "badan_usaha", "legal_name": "PT Lari Sehat Uji", "account_number": ""}), 200)
	if r := e.do(owner, "POST", base+"/submit", map[string]any{"consent": true}); r.field("account_number") == "" || r.field("bank_code") == "" {
		t.Fatalf("rekening wajib: %s", r.raw)
	}
	e.must(e.do(owner, "PUT", base, badanUsahaInput()), 200)

	pdf := samplePDF("CONTOH - TIDAK ASLI")
	jpg := sampleJPEG(t)
	e.must(e.uploadDoc(owner, slug, "ktp_pic", "KTP saya.jpg", jpg), 200)
	e.must(e.uploadDoc(owner, slug, "npwp", "npwp.png", pngFile(t)), 200)
	e.must(e.uploadDoc(owner, slug, "akta_pendirian", "akta.pdf", pdf), 200)
	if r := e.uploadDoc(owner, slug, "surat_keterangan", "sk.pdf", pdf); r.code != 422 {
		t.Fatalf("dokumen di luar matriks: %s", r.raw)
	}
	if r := e.uploadDoc(owner, slug, "sk_kemenkumham", "x.pdf", []byte("MZ bukan pdf")); r.code != 422 || r.body["error"] != "invalid_document" {
		t.Fatalf("exe: %s", r.raw)
	}
	big := append(samplePDF("besar"), make([]byte, 5<<20)...)
	if r := e.uploadDoc(owner, slug, "sk_kemenkumham", "besar.pdf", big); r.code != 413 || r.body["error"] != "file_too_large" {
		t.Fatalf("> 5 MB: %d %s", r.code, r.raw)
	}
	e.must(e.uploadDoc(owner, slug, "sk_kemenkumham", "sk.pdf", pdf), 200)

	// Owner melihat berkasnya sendiri (tercatat), JPEG tanpa EXIF.
	rec := e.get(owner, base+"/documents/ktp_pic")
	if rec.Code != 200 || bytes.Contains(rec.Body.Bytes(), []byte("Exif")) || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("unduh ktp owner: %d", rec.Code)
	}

	st = e.must(e.do(owner, "POST", base+"/submit", map[string]any{"consent": true}), 200)
	v = st.body["verification"].(map[string]any)
	firstID := v["id"].(string)
	if v["status"] != "submitted" || v["consent_version"] == nil {
		t.Fatalf("submit: %s", st.raw)
	}
	// Terkunci setelah dikirim.
	if r := e.do(owner, "PUT", base, badanUsahaInput()); r.code != 409 || r.body["error"] != "not_editable" {
		t.Fatalf("ubah setelah submit: %s", r.raw)
	}
	if r := e.uploadDoc(owner, slug, "nib", "nib.pdf", pdf); r.code != 409 {
		t.Fatalf("unggah setelah submit: %s", r.raw)
	}
	if r := e.do(owner, "DELETE", base+"/documents/akta_pendirian", nil); r.code != 409 {
		t.Fatalf("hapus setelah submit: %s", r.raw)
	}

	// Tidak ada plaintext di penyimpanan.
	var picEnc, npwpEnc, accEnc string
	e.db.QueryRow(ctx, `SELECT pic_id_enc, npwp_enc, account_enc FROM org_verifications WHERE id = $1`, firstID).Scan(&picEnc, &npwpEnc, &accEnc) //nolint:errcheck
	for _, c := range []string{picEnc, npwpEnc, accEnc} {
		if !strings.HasPrefix(c, "v1:") || strings.Contains(c, testNIK) || strings.Contains(c, testNPWP) || strings.Contains(c, testAccount) {
			t.Fatalf("kolom terenkripsi berisi plaintext: %q", c[:8])
		}
	}
	files, _ := os.ReadDir(e.dir)
	if len(files) != 4 {
		t.Fatalf("jumlah berkas %d, mau 4 (unggahan ditolak tidak boleh tertinggal)", len(files))
	}
	for _, f := range files {
		raw, _ := os.ReadFile(filepath.Join(e.dir, f.Name()))
		for _, magic := range [][]byte{[]byte("%PDF-"), {0xff, 0xd8}, []byte("\x89PNG")} {
			if bytes.HasPrefix(raw, magic) {
				t.Fatalf("berkas %s plaintext", f.Name())
			}
		}
		if !bytes.HasPrefix(raw, []byte("v1:")) || bytes.Contains(raw, []byte("CONTOH")) || bytes.Contains(raw, []byte("%PDF")) {
			t.Fatalf("berkas %s tidak terenkripsi", f.Name())
		}
	}

	// Belum terverifikasi: event tidak bisa terbit.
	now := time.Now()
	ev := e.must(e.do(owner, "POST", "/v1/org/"+slug+"/events", eventBody("Event KYC", "Bogor", now.Add(time.Hour), now.AddDate(0, 0, 30), cat("5K", "5", 10))), 201)
	evID := ev.body["id"].(string)
	if r := e.do(owner, "POST", "/v1/org/"+slug+"/events/"+evID+"/publish", nil); r.body["error"] != "org_not_verified" {
		t.Fatalf("terbit sebelum verifikasi: %s", r.raw)
	}

	// Super admin meninjau.
	sa := e.superAdmin("sa")
	if r := e.must(e.do(sa, "GET", "/v1/auth/session", nil), 200); r.body["me"].(map[string]any)["is_super_admin"] != true {
		t.Fatalf("session tanpa is_super_admin: %s", r.raw)
	}
	list := e.must(e.do(sa, "GET", "/v1/admin/verifications?q="+e.tag, nil), 200)
	if !strings.Contains(list.raw, firstID) || list.body["counts"].(map[string]any)["submitted"].(float64) < 1 {
		t.Fatalf("antrean: %s", list.raw)
	}
	adm := "/v1/admin/verifications/" + firstID
	det := e.must(e.do(sa, "GET", adm, nil), 200)
	if strings.Contains(det.raw, testNIK) || strings.Contains(det.raw, testAccount) {
		t.Fatal("detail admin tidak tersamar")
	}
	if nm := det.body["name_match"].(map[string]any); nm["ok"] != true {
		t.Fatalf("kecocokan nama: %v", nm)
	}
	if len(det.body["documents"].([]any)) != 4 || len(det.body["members"].([]any)) != 1 || det.body["conflict_of_interest"] != false {
		t.Fatalf("detail: %s", det.raw)
	}
	if r := e.must(e.do(sa, "POST", adm+"/reveal", map[string]any{"field": "pic_id"}), 200); r.body["value"] != testNIK {
		t.Fatal("reveal NIK salah")
	}
	if r := e.must(e.do(sa, "POST", adm+"/reveal", map[string]any{"field": "account"}), 200); r.body["value"] != testAccount {
		t.Fatal("reveal rekening salah")
	}
	if r := e.do(sa, "POST", adm+"/reveal", map[string]any{"field": "password"}); r.code != 422 {
		t.Fatalf("reveal kolom asing: %s", r.raw)
	}
	rec = e.get(sa, adm+"/documents/akta_pendirian")
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), pdf) {
		t.Fatalf("unduh akta admin: %d", rec.Code)
	}
	for k, want := range map[string]string{
		"Content-Type": "application/pdf", "X-Content-Type-Options": "nosniff", "Cache-Control": "private, no-store",
		"Content-Security-Policy": "sandbox; default-src 'none'", "Content-Disposition": "inline; filename*=UTF-8''akta.pdf",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("header %s = %q, mau %q", k, got, want)
		}
	}
	if rec := e.get(sa, adm+"/documents/nib"); rec.Code != 404 {
		t.Fatalf("dokumen tidak ada: %d", rec.Code)
	}

	// Tolak: alasan wajib 10–1000 karakter.
	if r := e.do(sa, "POST", adm+"/reject", map[string]any{"reason": "buram"}); r.code != 422 || r.field("reason") == "" {
		t.Fatalf("alasan pendek: %s", r.raw)
	}
	if r := e.do(sa, "POST", adm+"/reject", map[string]any{"reason": strings.Repeat("x", 1001)}); r.code != 422 {
		t.Fatalf("alasan panjang: %s", r.raw)
	}
	reason := "Foto KTP buram, mohon unggah ulang yang terbaca jelas."
	e.must(e.do(sa, "POST", adm+"/reject", map[string]any{"reason": reason}), 200)
	if r := e.do(sa, "POST", adm+"/approve", nil); r.code != 409 || r.body["error"] != "invalid_status" {
		t.Fatalf("setujui setelah ditolak: %s", r.raw)
	}
	prof := e.must(e.do(owner, "GET", "/v1/org/"+slug+"/profile", nil), 200)
	if prof.body["kyc_status"] != "rejected" || prof.body["reject_reason"] != reason || prof.body["verification_status"] != "rejected" {
		t.Fatalf("profil setelah ditolak: %s", prof.raw)
	}
	if r := e.do(owner, "POST", "/v1/org/"+slug+"/events/"+evID+"/publish", nil); r.body["error"] != "org_not_verified" {
		t.Fatalf("terbit setelah ditolak: %s", r.raw)
	}
	if r := e.do(owner, "PUT", base, badanUsahaInput()); r.code != 409 || r.body["error"] != "reopen_required" {
		t.Fatalf("ubah setelah ditolak tanpa ajukan ulang: %s", r.raw)
	}

	// Ajukan ulang: salinan isian & dokumen.
	st = e.must(e.do(owner, "POST", base+"/reopen", nil), 200)
	v = st.body["verification"].(map[string]any)
	if v["attempt"].(float64) != 2 || v["status"] != "draft" || v["pic_id"] != "••••0123" || len(v["documents"].([]any)) != 4 || v["consent_at"] != nil {
		t.Fatalf("ajukan ulang: %s", st.raw)
	}
	if len(st.body["history"].([]any)) != 2 {
		t.Fatalf("riwayat: %s", st.raw)
	}
	if r := e.do(owner, "POST", base+"/reopen", nil); r.code != 409 {
		t.Fatalf("ajukan ulang dua kali: %s", r.raw)
	}
	if rec := e.get(owner, base+"/documents/akta_pendirian"); rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), pdf) {
		t.Fatalf("salinan dokumen tidak bisa didekripsi: %d", rec.Code)
	}
	// Hapus dokumen salinan: berkas pengajuan lama tetap ada.
	e.must(e.do(owner, "DELETE", base+"/documents/ktp_pic", nil), 200)
	if files, _ := os.ReadDir(e.dir); len(files) != 4 {
		t.Fatalf("berkas lama ikut terhapus: %d", len(files))
	}
	if rec := e.get(sa, adm+"/documents/ktp_pic"); rec.Code != 200 {
		t.Fatalf("dokumen pengajuan lama: %d", rec.Code)
	}
	e.must(e.uploadDoc(owner, slug, "ktp_pic", "ktp-baru.jpg", sampleJPEG(t)), 200)
	// Ganti berkas di draft: berkas yang hanya dirujuk draft ini dihapus.
	e.must(e.uploadDoc(owner, slug, "ktp_pic", "ktp-baru-2.jpg", sampleJPEG(t)), 200)
	if files, _ := os.ReadDir(e.dir); len(files) != 5 {
		t.Fatalf("berkas pengganti: %d", len(files))
	}
	st = e.must(e.do(owner, "POST", base+"/submit", map[string]any{"consent": true}), 200)
	secondID := st.body["verification"].(map[string]any)["id"].(string)
	e.must(e.do(sa, "POST", "/v1/admin/verifications/"+secondID+"/approve", nil), 200)
	if r := e.must(e.do(owner, "GET", base, nil), 200); r.body["kyc_status"] != "verified" {
		t.Fatalf("setelah disetujui: %s", r.raw)
	}
	e.must(e.do(owner, "POST", "/v1/org/"+slug+"/events/"+evID+"/publish", nil), 200)
	if r := e.do(owner, "PUT", base, badanUsahaInput()); r.code != 409 || r.body["error"] != "already_verified" {
		t.Fatalf("ubah setelah terverifikasi: %s", r.raw)
	}

	// Audit: semua aksi tercatat, tanpa nilai sensitif.
	for action, min := range map[string]int{"kyc.submitted": 2, "kyc.document.view": 4, "kyc.field.reveal": 2, "kyc.rejected": 1, "kyc.approved": 1, "kyc.reopened": 1, "kyc.document.upload": 6} {
		if n := e.countAudit(slug, action); n < min {
			t.Errorf("audit %s: %d, mau ≥ %d", action, n, min)
		}
	}
	var reveal struct{ Actor, Field, IP string }
	e.db.QueryRow(ctx, `SELECT a.actor_id::text, a.after->>'field', a.after->>'ip' FROM audit_logs a WHERE a.action = 'kyc.field.reveal' AND a.entity_id = $1
		ORDER BY a.id LIMIT 1`, firstID).Scan(&reveal.Actor, &reveal.Field, &reveal.IP) //nolint:errcheck
	if reveal.Actor != sa.String() || reveal.Field != "pic_id" || reveal.IP == "" {
		t.Fatalf("audit reveal: %+v", reveal)
	}
	var views string
	e.db.QueryRow(ctx, `SELECT string_agg(after->>'by' || ':' || (after->>'kind'), ',' ORDER BY id) FROM audit_logs WHERE action = 'kyc.document.view' AND entity_id = $1`, firstID).Scan(&views) //nolint:errcheck
	if !strings.Contains(views, "owner:ktp_pic") || !strings.Contains(views, "super_admin:akta_pendirian") {
		t.Fatalf("audit view: %s", views)
	}
	var auditText string
	e.db.QueryRow(ctx, `SELECT string_agg(coalesce(after::text, ''), ' ') FROM audit_logs a JOIN organizations o ON o.id = a.org_id WHERE o.slug = $1`, slug).Scan(&auditText) //nolint:errcheck
	for _, s := range []string{testNIK, testNPWP, testAccount} {
		if strings.Contains(auditText, s) {
			t.Fatal("audit berisi nilai sensitif")
		}
	}

	// Email lewat outbox: submit, reject, submit, approve — tanpa nilai sensitif.
	rows, _ := e.db.Query(ctx, `SELECT payload->>'event', payload::text FROM outbox WHERE topic = 'kyc.email'
		AND payload->>'verification_id' IN ($1, $2) ORDER BY id`, firstID, secondID)
	var events []string
	for rows.Next() {
		var ev, p string
		rows.Scan(&ev, &p) //nolint:errcheck
		events = append(events, ev)
		if strings.Contains(p, testNIK) || strings.Contains(p, testAccount) {
			t.Fatal("outbox berisi nilai sensitif")
		}
	}
	if strings.Join(events, ",") != "submitted,rejected,submitted,approved" {
		t.Fatalf("email outbox: %v", events)
	}
	subject, body := notify.VerificationEmail("Org", "rejected", reason, "http://x/org/a/verifikasi")
	if !strings.Contains(body, "Foto KTP buram") || strings.Contains(subject+body, testNIK) {
		t.Fatal("isi email penolakan")
	}

	if s := logs.String(); strings.Contains(s, testNIK) || strings.Contains(s, testAccount) || strings.Contains(s, testNPWP) {
		t.Fatal("log aplikasi berisi nilai sensitif")
	}
}

func TestKYCTypesMatrix(t *testing.T) {
	e := newKYCEnv(t, false)
	pdf := samplePDF("CONTOH")
	sa := e.superAdmin("sa")
	for _, c := range []struct {
		typ, holder string
		required    []string
		notAllowed  string
	}{
		{"perorangan", "Sari Contoh", []string{"ktp_pic"}, "akta_pendirian"},
		{"komunitas", "Komunitas Uji", []string{"ktp_pic", "surat_keterangan"}, "sk_kemenkumham"},
	} {
		owner := e.user("own-" + c.typ)
		slug := e.createOrg(owner, "Org "+c.typ+" "+e.tag)
		base := "/v1/org/" + slug + "/verification"
		in := map[string]any{"org_type": c.typ, "legal_name": "Komunitas Uji", "address": "Jl. Contoh No. 2, Bogor",
			"pic_name": "Sari Contoh", "pic_phone": "081234567891", "pic_id": testNIK, "bank_code": "lainnya",
			"bank_name": "Bank Contoh", "account_number": testAccount, "account_holder": c.holder}
		e.must(e.do(owner, "PUT", base, in), 200)
		if r := e.uploadDoc(owner, slug, c.notAllowed, "x.pdf", pdf); r.code != 422 {
			t.Fatalf("%s: %s seharusnya ditolak: %s", c.typ, c.notAllowed, r.raw)
		}
		r := e.do(owner, "POST", base+"/submit", map[string]any{"consent": true})
		for _, k := range c.required {
			if r.field("documents."+k) == "" {
				t.Fatalf("%s: %s wajib: %s", c.typ, k, r.raw)
			}
		}
		if r.field("npwp") != "" || r.field("documents.npwp") != "" {
			t.Fatalf("%s: NPWP opsional: %s", c.typ, r.raw)
		}
		for _, k := range c.required {
			e.must(e.uploadDoc(owner, slug, k, k+".pdf", pdf), 200)
		}
		st := e.must(e.do(owner, "POST", base+"/submit", map[string]any{"consent": true}), 200)
		id := st.body["verification"].(map[string]any)["id"].(string)
		det := e.must(e.do(sa, "GET", "/v1/admin/verifications/"+id, nil), 200)
		if det.body["name_match"].(map[string]any)["ok"] != true {
			t.Fatalf("%s: kecocokan nama: %s", c.typ, det.raw)
		}
		e.must(e.do(sa, "POST", "/v1/admin/verifications/"+id+"/approve", nil), 200)
	}

	// Ganti jenis membuang dokumen yang tidak dipakai jenis baru.
	owner := e.user("ganti")
	slug := e.createOrg(owner, "Org Ganti "+e.tag)
	base := "/v1/org/" + slug + "/verification"
	e.must(e.do(owner, "PUT", base, map[string]any{"org_type": "badan_usaha"}), 200)
	e.must(e.uploadDoc(owner, slug, "akta_pendirian", "akta.pdf", pdf), 200)
	e.must(e.uploadDoc(owner, slug, "ktp_pic", "ktp.pdf", pdf), 200)
	st := e.must(e.do(owner, "PUT", base, map[string]any{"org_type": "perorangan"}), 200)
	docs := st.body["verification"].(map[string]any)["documents"].([]any)
	if len(docs) != 1 || docs[0].(map[string]any)["kind"] != "ktp_pic" {
		t.Fatalf("dokumen setelah ganti jenis: %s", st.raw)
	}
}

func TestKYCAuthorization(t *testing.T) {
	e := newKYCEnv(t, false)
	pdf := samplePDF("CONTOH")
	ownerA, ownerB := e.user("ownerA"), e.user("ownerB")
	a, b := e.createOrg(ownerA, "Org A "+e.tag), e.createOrg(ownerB, "Org B "+e.tag)
	baseA := "/v1/org/" + a + "/verification"
	in := badanUsahaInput()
	e.must(e.do(ownerA, "PUT", baseA, in), 200)
	for _, k := range []string{"ktp_pic", "npwp", "akta_pendirian", "sk_kemenkumham"} {
		e.must(e.uploadDoc(ownerA, a, k, k+".pdf", pdf), 200)
	}
	st := e.must(e.do(ownerA, "POST", baseA+"/submit", map[string]any{"consent": true}), 200)
	adm := "/v1/admin/verifications/" + st.body["verification"].(map[string]any)["id"].(string)

	// Anonim → 401.
	for _, p := range []string{"/v1/admin/verifications", adm, baseA, baseA + "/documents/ktp_pic"} {
		if r := e.do(uuid.Nil, "GET", p, nil); r.code != 401 {
			t.Fatalf("anonim %s: %d", p, r.code)
		}
	}
	// Pelari biasa → 404 di semua rute admin, termasuk dokumen & reveal walau tahu ID.
	runner := e.user("pelari")
	for _, c := range []struct{ m, p string }{{"GET", "/v1/admin/verifications"}, {"GET", adm}, {"POST", adm + "/reveal"},
		{"GET", adm + "/documents/ktp_pic"}, {"POST", adm + "/approve"}, {"POST", adm + "/reject"}} {
		if r := e.do(runner, c.m, c.p, map[string]any{"field": "pic_id", "reason": "alasan yang cukup panjang"}); r.code != 404 {
			t.Fatalf("pelari %s %s: %d", c.m, c.p, r.code)
		}
	}
	// Owner org B → 404 untuk org A; dokumen org A tidak bisa diambil lewat org sendiri.
	if r := e.do(ownerB, "GET", baseA, nil); r.code != 404 {
		t.Fatalf("owner B ke A: %d", r.code)
	}
	if rec := e.get(ownerB, baseA+"/documents/ktp_pic"); rec.Code != 404 {
		t.Fatalf("owner B unduh dokumen A: %d", rec.Code)
	}
	if rec := e.get(ownerB, "/v1/org/"+b+"/verification/documents/ktp_pic"); rec.Code != 404 {
		t.Fatalf("owner B unduh ktp lewat org sendiri: %d", rec.Code)
	}
	// Peran lain di org A → ditolak.
	for _, role := range []string{"admin", "finance", "rpc_staff"} {
		u := e.user("staf-" + role)
		e.must(e.do(ownerA, "POST", "/v1/org/"+a+"/members", map[string]any{"email": e.email("staf-" + role), "role": role}), 201)
		if r := e.do(u, "GET", baseA, nil); r.code != 403 {
			t.Fatalf("%s ke /verification: %d", role, r.code)
		}
		if rec := e.get(u, baseA+"/documents/ktp_pic"); rec.Code != 403 {
			t.Fatalf("%s unduh dokumen: %d", role, rec.Code)
		}
		if r := e.do(u, "GET", "/v1/org/"+a+"/profile", nil); r.code != 200 || r.body["verification_status"] != "submitted" {
			t.Fatalf("%s lihat status: %s", role, r.raw)
		}
	}
	// Super admin bukan anggota → endpoint mitra 404.
	sa := e.superAdmin("sa")
	if r := e.do(sa, "GET", baseA, nil); r.code != 404 {
		t.Fatalf("super admin ke endpoint mitra: %d", r.code)
	}
	// Super admin yang anggota org → conflict_of_interest.
	saMember := e.superAdmin("sa-anggota")
	e.must(e.do(ownerA, "POST", "/v1/org/"+a+"/members", map[string]any{"email": e.email("sa-anggota"), "role": "finance"}), 201)
	for _, action := range []string{"/approve", "/reject"} {
		if r := e.do(saMember, "POST", adm+action, map[string]any{"reason": "alasan yang cukup panjang"}); r.code != 403 || r.body["error"] != "conflict_of_interest" {
			t.Fatalf("benturan kepentingan %s: %s", action, r.raw)
		}
	}
	if r := e.must(e.do(saMember, "GET", adm, nil), 200); r.body["conflict_of_interest"] != true {
		t.Fatalf("penanda benturan: %s", r.raw)
	}
	// Draft tidak terlihat oleh admin.
	baseB := "/v1/org/" + b + "/verification"
	draft := e.must(e.do(ownerB, "PUT", baseB, map[string]any{"org_type": "perorangan"}), 200)
	if r := e.do(sa, "GET", "/v1/admin/verifications/"+draft.body["verification"].(map[string]any)["id"].(string), nil); r.code != 404 {
		t.Fatalf("draft terlihat admin: %d", r.code)
	}
	if r := e.do(sa, "GET", "/v1/admin/verifications/bukan-uuid", nil); r.code != 404 {
		t.Fatalf("id rusak: %d", r.code)
	}
	// Tidak ada rute statis ke berkas KYC.
	files, _ := os.ReadDir(e.dir)
	if rec := e.get(ownerA, "/uploads/"+files[0].Name()); rec.Code != 404 {
		t.Fatalf("berkas KYC lewat /uploads: %d", rec.Code)
	}
}

func TestKYCAutoVerify(t *testing.T) {
	e := newKYCEnv(t, true)
	owner := e.user("owner")
	slug := e.createOrg(owner, "Org Auto "+e.tag)
	st := e.must(e.do(owner, "GET", "/v1/org/"+slug+"/verification", nil), 200)
	if st.body["kyc_status"] != "verified" || st.body["auto_verify"] != true || st.body["verification"] != nil {
		t.Fatalf("auto verify: %s", st.raw)
	}
	if r := e.do(owner, "PUT", "/v1/org/"+slug+"/verification", map[string]any{"org_type": "perorangan"}); r.code != 409 || r.body["error"] != "already_verified" {
		t.Fatalf("isi pengajuan saat auto verify: %s", r.raw)
	}
}

func TestKYCUploadRateLimit(t *testing.T) {
	e := newKYCEnv(t, false)
	owner := e.user("owner")
	slug := e.createOrg(owner, "Org Limit "+e.tag)
	for i := 0; i < 30; i++ {
		if r := e.uploadDoc(owner, slug, "ktp_pic", "x.txt", []byte("teks")); r.code == 429 {
			t.Fatalf("dibatasi terlalu cepat di unggahan ke-%d", i+1)
		}
	}
	if r := e.uploadDoc(owner, slug, "ktp_pic", "x.txt", []byte("teks")); r.code != 429 || r.body["retry_after"] == nil {
		t.Fatalf("unggahan ke-31: %s", r.raw)
	}
}

func TestKYCWrongKey(t *testing.T) {
	e := newKYCEnv(t, false)
	owner := e.user("owner")
	slug := e.createOrg(owner, "Org Kunci "+e.tag)
	base := "/v1/org/" + slug + "/verification"
	e.must(e.do(owner, "PUT", base, badanUsahaInput()), 200)
	e.must(e.uploadDoc(owner, slug, "akta_pendirian", "akta.pdf", samplePDF("CONTOH")), 200)

	other := make([]byte, 32)
	rand.Read(other) //nolint:errcheck
	box, _ := secretbox.New(other)
	e.h = e.router(box)
	rec := e.get(owner, base+"/documents/akta_pendirian")
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "decrypt_failed") || bytes.Contains(rec.Body.Bytes(), []byte("%PDF")) {
		t.Fatalf("kunci salah: %d %s", rec.Code, rec.Body.String())
	}
}
