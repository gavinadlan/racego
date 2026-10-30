package finance

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DisburseRequest: satu transfer ke rekening mitra. AccountNumber hanya hidup di memori.
type DisburseRequest struct {
	ID            string // payouts.id: kunci idempotensi ke gateway
	Amount        int64
	BankCode      string // kode bank internal (internal/kyc.Banks)
	AccountNumber string
	AccountHolder string
	Notes         string
}

type DisburseState string

const (
	DisbursePending   DisburseState = "pending" // masih diproses gateway
	DisburseCompleted DisburseState = "completed"
	DisburseFailed    DisburseState = "failed"
)

type DisburseStatus struct {
	State  DisburseState
	Reason string // alasan singkat bila gagal
}

// Disburser mengirim uang ke rekening mitra. Implementasi: MockDisburser, Iris (Midtrans Payouts).
type Disburser interface {
	// Create membuat (dan menyetujui) transfer; mengembalikan nomor referensi gateway.
	// Aman diulang dengan req.ID yang sama. Error: *PermanentError (ditolak, jangan diulang)
	// atau error lain (sementara: timeout/5xx/respons rusak → diulang dengan backoff).
	Create(ctx context.Context, req DisburseRequest) (string, error)
	Status(ctx context.Context, ref string) (DisburseStatus, error)
}

// PermanentError: gateway menolak permintaan; mengulang tidak akan berhasil.
type PermanentError struct{ Reason string }

func (e *PermanentError) Error() string { return "ditolak gateway: " + e.Reason }

func isPermanent(err error) (string, bool) {
	var pe *PermanentError
	if errors.As(err, &pe) {
		return pe.Reason, true
	}
	return "", false
}

// MockFailSuffix: rekening berakhiran ini selalu gagal di mode mock (untuk menguji alur gagal).
const MockFailSuffix = "0000"

// MockDisburser: tanpa panggilan jaringan. Status dihitung dari waktu di nomor referensi, jadi
// tetap benar meski API dan worker proses berbeda atau di-restart:
// processing → completed setelah Delay, atau failed bila rekening berakhiran MockFailSuffix.
type MockDisburser struct {
	Delay time.Duration
	Now   func() time.Time // nil = time.Now
}

func (m MockDisburser) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m MockDisburser) Create(_ context.Context, req DisburseRequest) (string, error) {
	kind := "ok"
	if strings.HasSuffix(req.AccountNumber, MockFailSuffix) {
		kind = "fail"
	}
	return fmt.Sprintf("mock-%s-%d-%s", kind, m.now().UnixMilli(), req.ID), nil
}

func (m MockDisburser) Status(_ context.Context, ref string) (DisburseStatus, error) {
	parts := strings.SplitN(ref, "-", 4)
	if len(parts) != 4 || parts[0] != "mock" {
		return DisburseStatus{}, fmt.Errorf("referensi mock tidak dikenal: %q", ref)
	}
	ms, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return DisburseStatus{}, fmt.Errorf("referensi mock tidak dikenal: %q", ref)
	}
	if m.now().Before(time.UnixMilli(ms).Add(m.Delay)) {
		return DisburseStatus{State: DisbursePending}, nil
	}
	if parts[1] == "fail" {
		return DisburseStatus{State: DisburseFailed, Reason: "Simulasi: rekening tujuan menolak transfer (mode mock)"}, nil
	}
	return DisburseStatus{State: DisburseCompleted}, nil
}
