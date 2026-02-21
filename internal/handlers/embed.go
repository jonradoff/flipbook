package handlers

import (
	"html/template"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/storage"
)

type EmbedHandler struct {
	db      *database.DB
	storage *storage.Storage
	tmpl    *template.Template
	baseURL string
}

func NewEmbedHandler(db *database.DB, store *storage.Storage, tmpl *template.Template, baseURL string) *EmbedHandler {
	return &EmbedHandler{db: db, storage: store, tmpl: tmpl, baseURL: baseURL}
}

func (h *EmbedHandler) Embed(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	fb, err := h.db.GetFlipbookBySlug(slug)
	if err != nil {
		http.Error(w, "Flipbook not found", 404)
		return
	}

	w.Header().Set("X-Frame-Options", "ALLOWALL")
	w.Header().Set("Content-Security-Policy", "frame-ancestors *")

	switch fb.Status {
	case models.StatusReady:
		// continue below
	case models.StatusRegenerating, models.StatusConverting, models.StatusPending:
		h.tmpl.ExecuteTemplate(w, "viewer_wait", map[string]interface{}{
			"Flipbook": fb,
			"BaseURL":  h.baseURL,
		})
		return
	default:
		http.Error(w, "Flipbook not found", 404)
		return
	}

	go h.db.RecordView(fb.ID, r.Referer(), r.UserAgent())

	pageFmt := h.storage.DetectPageFormat(fb.ID)
	var pages []string
	for i := 1; i <= fb.PageCount; i++ {
		pages = append(pages, h.storage.PageImageURL(fb.ID, pageFmt, i))
	}

	h.tmpl.ExecuteTemplate(w, "embed", map[string]interface{}{
		"Flipbook": fb,
		"Pages":    pages,
		"BaseURL":  h.baseURL,
	})
}
