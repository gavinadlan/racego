// Package payment mengintegrasikan Midtrans Snap: membuat transaksi dan memproses webhook.
package payment

import (
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
)

// Notification adalah HTTP notification (webhook) dari Midtrans.
// Field lain tetap disimpan utuh di kolom payload.
type Notification struct {
	OrderID           string `json:"order_id"`
	TransactionID     string `json:"transaction_id"`
	TransactionStatus string `json:"transaction_status"`
	FraudStatus       string `json:"fraud_status"`
	StatusCode        string `json:"status_code"`
	GrossAmount       string `json:"gross_amount"`
	PaymentType       string `json:"payment_type"`
	SignatureKey      string `json:"signature_key"`
}

// Signature menghitung signature_key Midtrans:
// SHA512(order_id + status_code + gross_amount + server_key), hex.
func Signature(orderID, statusCode, grossAmount, serverKey string) string {
	sum := sha512.Sum512([]byte(orderID + statusCode + grossAmount + serverKey))
	return hex.EncodeToString(sum[:])
}

// Verify membandingkan signature secara constant-time.
func (n Notification) Verify(serverKey string) bool {
	want := Signature(n.OrderID, n.StatusCode, n.GrossAmount, serverKey)
	return subtle.ConstantTimeCompare([]byte(want), []byte(n.SignatureKey)) == 1
}

// IsPaid: lunas hanya bila settlement, atau capture kartu yang lolos fraud check.
func (n Notification) IsPaid() bool {
	switch n.TransactionStatus {
	case "settlement":
		return true
	case "capture":
		return n.FraudStatus == "accept"
	}
	return false
}

// IsFailed: pembayaran tidak akan terjadi; slot harus dikembalikan.
func (n Notification) IsFailed() bool {
	switch n.TransactionStatus {
	case "expire", "cancel", "deny", "failure":
		return true
	}
	return false
}

// AmountRupiah mengubah gross_amount ("350000.00") ke rupiah bulat.
func (n Notification) AmountRupiah() (int64, error) {
	f, err := strconv.ParseFloat(n.GrossAmount, 64)
	if err != nil {
		return 0, fmt.Errorf("gross_amount tidak valid: %q", n.GrossAmount)
	}
	return int64(math.Round(f)), nil
}

// FormatAmount menulis nominal dalam format yang dipakai Midtrans.
func FormatAmount(rupiah int64) string { return strconv.FormatInt(rupiah, 10) + ".00" }
