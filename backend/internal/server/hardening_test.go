package server

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Pratinjau PDF di Chrome meminta berkas dua kali: (aktor, dokumen) sama dalam 10 detik = satu baris audit.
// Lewat 10 detik atau aktor lain = baris baru.
func TestKYCDocumentViewAuditDedupe(t *testing.T) {
	e := newKYCEnv(t, false)
	ctx := context.Background()

	owner := e.user("owner")
	slug := e.createOrg(owner, "Org Dedupe "+e.tag)
	base := "/v1/org/" + slug + "/verification"
	e.must(e.do(owner, "PUT", base, badanUsahaInput()), 200)
	e.must(e.uploadDoc(owner, slug, "akta_pendirian", "akta.pdf", samplePDF("CONTOH")), 200)
	e.must(e.uploadDoc(owner, slug, "npwp", "npwp.pdf", samplePDF("CONTOH")), 200)
	owner2 := e.user("owner2")
	if _, err := e.db.Exec(ctx, `INSERT INTO org_members (org_id, user_id, role) SELECT id, $2, 'owner' FROM organizations WHERE slug = $1`, slug, owner2); err != nil {
		t.Fatal(err)
	}

	view := func(u uuid.UUID, kind string) {
		t.Helper()
		if rec := e.get(u, base+"/documents/"+kind); rec.Code != 200 {
			t.Fatalf("lihat %s: %d", kind, rec.Code)
		}
	}
	expect := func(want int, step string) {
		t.Helper()
		if n := e.countAudit(slug, "kyc.document.view"); n != want {
			t.Fatalf("%s: %d baris audit, mau %d", step, n, want)
		}
	}
	view(owner, "akta_pendirian")
	view(owner, "akta_pendirian")
	expect(1, "dua permintaan berurutan")
	view(owner, "npwp")
	expect(2, "dokumen lain")
	view(owner2, "akta_pendirian")
	expect(3, "aktor lain")
	e.mr.FastForward(11 * time.Second)
	view(owner, "akta_pendirian")
	expect(4, "11 detik kemudian")
}
