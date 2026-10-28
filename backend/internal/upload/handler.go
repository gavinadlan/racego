package upload

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"github.com/gavinadlan/racego/backend/internal/httpx"
)

type Handler struct{ store Storage }

func NewHandler(store Storage) *Handler { return &Handler{store: store} }

// Upload: POST /v1/org/{org}/uploads?kind=cover|logo (multipart, field "file") → {"url":"/uploads/<hash>.jpg"}.
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	if k := r.URL.Query().Get("kind"); k != "cover" && k != "logo" {
		httpx.Error(w, http.StatusUnprocessableEntity, "invalid_kind", "Parameter kind harus cover atau logo")
		return
	}
	// Sisakan ruang untuk header multipart; ukuran file sendiri dicek tepat di bawah.
	r.Body = http.MaxBytesReader(w, r.Body, MaxBytes+64<<10)
	f, _, err := r.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			httpx.Error(w, http.StatusRequestEntityTooLarge, "file_too_large", "Ukuran file maksimal 3 MB")
			return
		}
		httpx.Error(w, http.StatusBadRequest, "file_required", "Pilih file gambar untuk diunggah")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	name, clean, err := Process(data)
	switch {
	case errors.Is(err, ErrTooLarge):
		httpx.Error(w, http.StatusRequestEntityTooLarge, "file_too_large", "Ukuran file maksimal 3 MB")
		return
	case errors.Is(err, ErrNotImage):
		httpx.Error(w, http.StatusUnprocessableEntity, "invalid_image", "File harus gambar JPEG atau PNG")
		return
	case errors.Is(err, ErrTooBig):
		httpx.Error(w, http.StatusUnprocessableEntity, "image_too_big", "Sisi terpanjang gambar maksimal 4.000 piksel")
		return
	case err != nil:
		httpx.Internal(w, r, err)
		return
	}
	if err := h.store.Put(r.Context(), name, clean); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"url": "/uploads/" + name})
}

// Serve: GET /uploads/{name}. Nama = hash isi, jadi aman di-cache selamanya.
func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	f, mod, err := h.store.Open(r.Context(), name)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "baca upload", "name", name, "err", err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", contentTypes[filepath.Ext(name)])
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, mod, f)
}
