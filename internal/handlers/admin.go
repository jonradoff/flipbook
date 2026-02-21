package handlers

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/storage"
	"github.com/jonradoff/flipbook/internal/worker"
)

type AdminHandler struct {
	db      *database.DB
	storage *storage.Storage
	worker  *worker.Worker
	tmpl    *template.Template
	baseURL string
}

func NewAdminHandler(db *database.DB, store *storage.Storage, w *worker.Worker, tmpl *template.Template, baseURL string) *AdminHandler {
	return &AdminHandler{db: db, storage: store, worker: w, tmpl: tmpl, baseURL: baseURL}
}

func (h *AdminHandler) Index(w http.ResponseWriter, r *http.Request) {
	flipbooks, err := h.db.ListFlipbooks()
	if err != nil {
		http.Error(w, "Failed to list flipbooks", 500)
		return
	}
	// Build first-thumb URL map keyed by flipbook ID
	thumbMap := make(map[string]string)
	for _, fb := range flipbooks {
		if fb.Status == "ready" && fb.PageCount > 0 {
			pageFmt := h.storage.DetectPageFormat(fb.ID)
			thumbMap[fb.ID] = h.storage.ThumbImageURL(fb.ID, pageFmt, 1)
		}
	}
	h.tmpl.ExecuteTemplate(w, "admin_index", map[string]interface{}{
		"Flipbooks": flipbooks,
		"BaseURL":   h.baseURL,
		"ThumbMap":  thumbMap,
	})
}

func (h *AdminHandler) UploadForm(w http.ResponseWriter, r *http.Request) {
	h.tmpl.ExecuteTemplate(w, "admin_upload", nil)
}

func (h *AdminHandler) Upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20) // 100MB

	if err := r.ParseMultipartForm(100 << 20); err != nil {
		http.Error(w, "File too large (max 100MB)", 400)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "No file provided", 400)
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".pptx" && ext != ".ppt" && ext != ".pdf" {
		http.Error(w, "Only .pptx, .ppt, and .pdf files are supported", 400)
		return
	}

	id := uuid.New().String()
	title := r.FormValue("title")
	if title == "" {
		title = strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	}
	slug := slugify(title)
	slug = h.db.EnsureUniqueSlug(slug)

	srcPath, err := h.storage.SaveUpload(id, header.Filename, file)
	if err != nil {
		log.Printf("Failed to save upload: %v", err)
		http.Error(w, "Failed to save file", 500)
		return
	}

	fb := &models.Flipbook{
		ID:       id,
		Title:    title,
		Slug:     slug,
		Filename: header.Filename,
		FileSize: header.Size,
		Status:   models.StatusPending,
	}
	if err := h.db.CreateFlipbook(fb); err != nil {
		log.Printf("Failed to create flipbook record: %v", err)
		http.Error(w, "Failed to create flipbook", 500)
		return
	}

	h.worker.Enqueue(worker.Job{FlipbookID: id, SourcePath: srcPath})

	// Return JSON for AJAX requests so the client can track progress
	if r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"id":   id,
			"slug": slug,
		})
		return
	}

	http.Redirect(w, r, "/admin/flipbooks/"+id, http.StatusSeeOther)
}

func (h *AdminHandler) Detail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	fb, err := h.db.GetFlipbook(id)
	if err != nil {
		http.Error(w, "Flipbook not found", 404)
		return
	}

	views := h.db.GetViewCount(id)

	// Build page URLs for thumbnails
	pageFmt := h.storage.DetectPageFormat(id)
	var thumbs []string
	for i := 1; i <= fb.PageCount; i++ {
		thumbs = append(thumbs, h.storage.ThumbImageURL(id, pageFmt, i))
	}

	h.tmpl.ExecuteTemplate(w, "admin_detail", map[string]interface{}{
		"Flipbook":  fb,
		"Thumbs":    thumbs,
		"Views":     views,
		"BaseURL":   h.baseURL,
		"EmbedCode": embedCode(h.baseURL, fb.Slug),
	})
}

func (h *AdminHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	h.db.DeleteFlipbook(id)
	h.storage.DeleteFlipbook(id)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (h *AdminHandler) Settings(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	title := r.FormValue("title")
	desc := r.FormValue("description")
	if title != "" {
		h.db.UpdateFlipbook(id, title, desc)
	}
	http.Redirect(w, r, "/admin/flipbooks/"+id, http.StatusSeeOther)
}

func embedCode(baseURL, slug string) string {
	return `<iframe src="` + baseURL + `/embed/` + slug + `" width="800" height="600" frameborder="0" allowfullscreen style="border:none;border-radius:8px;box-shadow:0 2px 8px rgba(0,0,0,0.1);"></iframe>`
}

// Google Slides URL pattern: extract presentation ID
var googleSlidesRe = regexp.MustCompile(`/presentation/d/([a-zA-Z0-9_-]+)`)

func (h *AdminHandler) ImportURL(w http.ResponseWriter, r *http.Request) {
	url := strings.TrimSpace(r.FormValue("url"))
	if url == "" {
		http.Error(w, "No URL provided", 400)
		return
	}

	// Extract Google Slides presentation ID
	matches := googleSlidesRe.FindStringSubmatch(url)
	if len(matches) < 2 {
		http.Error(w, "Invalid Google Slides URL. Use a URL like: https://docs.google.com/presentation/d/PRESENTATION_ID/edit", 400)
		return
	}
	presentationID := matches[1]

	// Download as PDF via Google's export endpoint
	exportURL := fmt.Sprintf("https://docs.google.com/presentation/d/%s/export/pdf", presentationID)
	resp, err := http.Get(exportURL)
	if err != nil {
		log.Printf("Failed to download Google Slides: %v", err)
		http.Error(w, "Failed to download presentation. Make sure the presentation is publicly accessible (Anyone with the link).", 400)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		http.Error(w, "Failed to download presentation. Make sure the presentation is publicly accessible (Anyone with the link).", 400)
		return
	}

	id := uuid.New().String()
	title := r.FormValue("title")
	if title == "" {
		title = "Google Slides Import"
	}
	slug := slugify(title)
	slug = h.db.EnsureUniqueSlug(slug)

	// Save downloaded PDF
	srcPath, err := h.storage.SaveUpload(id, "import.pdf", resp.Body)
	if err != nil {
		log.Printf("Failed to save downloaded PDF: %v", err)
		http.Error(w, "Failed to save file", 500)
		return
	}

	fb := &models.Flipbook{
		ID:       id,
		Title:    title,
		Slug:     slug,
		Filename: "import.pdf",
		Status:   models.StatusPending,
	}
	if err := h.db.CreateFlipbook(fb); err != nil {
		log.Printf("Failed to create flipbook record: %v", err)
		http.Error(w, "Failed to create flipbook", 500)
		return
	}

	h.worker.Enqueue(worker.Job{FlipbookID: id, SourcePath: srcPath})

	if r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"id":   id,
			"slug": slug,
		})
		return
	}

	http.Redirect(w, r, "/admin/flipbooks/"+id, http.StatusSeeOther)
}

var nonAlphanumeric = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == ' ' {
			return r
		}
		return -1
	}, s)
	s = nonAlphanumeric.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "flipbook"
	}
	return s
}
