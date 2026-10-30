// Package httpx berisi helper JSON untuk handler HTTP.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

type ErrorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// Error menulis error terstruktur: code untuk mesin, message untuk manusia.
func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, ErrorBody{Error: code, Message: message})
}

// Internal mencatat error asli ke log dan mengembalikan pesan umum ke klien.
// Klien yang membatalkan request (pindah halaman) bukan error server: tidak dicatat.
func Internal(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
		return
	}
	slog.ErrorContext(r.Context(), "internal error", "path", r.URL.Path, "err", err)
	Error(w, http.StatusInternalServerError, "internal", "Terjadi kesalahan pada server")
}

// Decode membaca body JSON dengan batas ukuran 1 MB dan menolak field tak dikenal.
func Decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			Error(w, http.StatusRequestEntityTooLarge, "body_too_large", "Body request terlalu besar")
			return false
		}
		Error(w, http.StatusBadRequest, "invalid_json", "Body request tidak valid: "+err.Error())
		return false
	}
	return true
}

// Fields berisi pesan error per kolom form, mis. {"sale_close_at": "Harus setelah waktu buka"}.
type Fields map[string]string

// Validation menulis 422 {"error":"validation","message":…,"fields":{…}} agar form bisa
// menampilkan pesan di bawah kolom yang salah.
func Validation(w http.ResponseWriter, fields Fields) {
	JSON(w, http.StatusUnprocessableEntity, struct {
		ErrorBody
		Fields Fields `json:"fields"`
	}{ErrorBody{Error: "validation", Message: "Periksa kembali isian yang ditandai."}, fields})
}
