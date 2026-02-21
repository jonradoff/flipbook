package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/storage"
	"github.com/jonradoff/flipbook/internal/worker"
)

type APIHandler struct {
	db      *database.DB
	storage *storage.Storage
	worker  *worker.Worker
	baseURL string
}

func NewAPIHandler(db *database.DB, store *storage.Storage, w *worker.Worker, baseURL string) *APIHandler {
	return &APIHandler{db: db, storage: store, worker: w, baseURL: baseURL}
}

func (h *APIHandler) ListFlipbooks(w http.ResponseWriter, r *http.Request) {
	flipbooks, err := h.db.ListFlipbooks()
	if err != nil {
		jsonError(w, "Failed to list flipbooks", 500)
		return
	}

	type item struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Slug      string `json:"slug"`
		Status    string `json:"status"`
		PageCount int    `json:"page_count"`
		ViewerURL string `json:"viewer_url"`
		EmbedURL  string `json:"embed_url"`
		CreatedAt string `json:"created_at"`
	}

	items := make([]item, 0, len(flipbooks))
	for _, fb := range flipbooks {
		items = append(items, item{
			ID:        fb.ID,
			Title:     fb.Title,
			Slug:      fb.Slug,
			Status:    fb.Status,
			PageCount: fb.PageCount,
			ViewerURL: h.baseURL + "/v/" + fb.Slug,
			EmbedURL:  h.baseURL + "/embed/" + fb.Slug,
			CreatedAt: fb.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}
	jsonResponse(w, items)
}

func (h *APIHandler) GetFlipbook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	fb, err := h.db.GetFlipbook(id)
	if err != nil {
		jsonError(w, "Flipbook not found", 404)
		return
	}

	pageFmt := h.storage.DetectPageFormat(fb.ID)
	var pages []string
	for i := 1; i <= fb.PageCount; i++ {
		pages = append(pages, h.baseURL+h.storage.PageImageURL(fb.ID, pageFmt, i))
	}

	jsonResponse(w, map[string]interface{}{
		"id":         fb.ID,
		"title":      fb.Title,
		"slug":       fb.Slug,
		"status":     fb.Status,
		"page_count": fb.PageCount,
		"width":      fb.PageWidth,
		"height":     fb.PageHeight,
		"viewer_url": h.baseURL + "/v/" + fb.Slug,
		"embed_url":  h.baseURL + "/embed/" + fb.Slug,
		"embed_code": embedCode(h.baseURL, fb.Slug),
		"pages":      pages,
		"error":      fb.ErrorMessage,
		"created_at": fb.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

func (h *APIHandler) UploadFlipbook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20)

	if err := r.ParseMultipartForm(100 << 20); err != nil {
		jsonError(w, "File too large (max 100MB)", 400)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		jsonError(w, "No file provided", 400)
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".pptx" && ext != ".ppt" && ext != ".pdf" {
		jsonError(w, "Only .pptx, .ppt, and .pdf files are supported", 400)
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
		jsonError(w, "Failed to save file", 500)
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
		log.Printf("Failed to create flipbook: %v", err)
		jsonError(w, "Failed to create flipbook", 500)
		return
	}

	// Back up original to GridFS
	go func() {
		f, err := os.Open(srcPath)
		if err != nil {
			log.Printf("GridFS: failed to open original for %s: %v", id, err)
			return
		}
		defer f.Close()
		gridfsID, err := h.db.UploadToGridFS(context.Background(), header.Filename, f)
		if err != nil {
			log.Printf("GridFS: upload failed for %s: %v", id, err)
			return
		}
		if err := h.db.SetGridFSFileID(id, gridfsID); err != nil {
			log.Printf("GridFS: failed to save file ID for %s: %v", id, err)
			return
		}
		log.Printf("GridFS: backed up %s (%s)", id, header.Filename)
	}()

	h.worker.Enqueue(worker.Job{FlipbookID: id, SourcePath: srcPath})

	w.WriteHeader(http.StatusCreated)
	jsonResponse(w, map[string]interface{}{
		"id":         id,
		"title":      title,
		"slug":       slug,
		"status":     "pending",
		"status_url": h.baseURL + "/api/flipbooks/" + id + "/status",
	})
}

func (h *APIHandler) FlipbookStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	fb, err := h.db.GetFlipbook(id)
	if err != nil {
		jsonError(w, "Flipbook not found", 404)
		return
	}
	jsonResponse(w, map[string]interface{}{
		"id":         fb.ID,
		"status":     fb.Status,
		"slug":       fb.Slug,
		"page_count": fb.PageCount,
		"error":      fb.ErrorMessage,
	})
}

var apiGoogleSlidesRe = regexp.MustCompile(`/presentation/d/([a-zA-Z0-9_-]+)`)

func (h *APIHandler) ImportURL(w http.ResponseWriter, r *http.Request) {
	url := strings.TrimSpace(r.FormValue("url"))
	if url == "" {
		jsonError(w, "No URL provided", 400)
		return
	}

	matches := apiGoogleSlidesRe.FindStringSubmatch(url)
	if len(matches) < 2 {
		jsonError(w, "Invalid Google Slides URL", 400)
		return
	}
	presentationID := matches[1]

	exportURL := fmt.Sprintf("https://docs.google.com/presentation/d/%s/export/pdf", presentationID)
	resp, err := http.Get(exportURL)
	if err != nil {
		log.Printf("Failed to download Google Slides: %v", err)
		jsonError(w, "Failed to download presentation. Make sure it is publicly accessible.", 400)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		jsonError(w, "Failed to download presentation. Make sure it is publicly accessible.", 400)
		return
	}

	id := uuid.New().String()
	title := r.FormValue("title")
	if title == "" {
		title = "Google Slides Import"
	}
	slug := slugify(title)
	slug = h.db.EnsureUniqueSlug(slug)

	srcPath, err := h.storage.SaveUpload(id, "import.pdf", resp.Body)
	if err != nil {
		log.Printf("Failed to save downloaded PDF: %v", err)
		jsonError(w, "Failed to save file", 500)
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
		log.Printf("Failed to create flipbook: %v", err)
		jsonError(w, "Failed to create flipbook", 500)
		return
	}

	// Back up original to GridFS
	go func() {
		f, err := os.Open(srcPath)
		if err != nil {
			log.Printf("GridFS: failed to open original for %s: %v", id, err)
			return
		}
		defer f.Close()
		gridfsID, err := h.db.UploadToGridFS(context.Background(), "import.pdf", f)
		if err != nil {
			log.Printf("GridFS: upload failed for %s: %v", id, err)
			return
		}
		if err := h.db.SetGridFSFileID(id, gridfsID); err != nil {
			log.Printf("GridFS: failed to save file ID for %s: %v", id, err)
			return
		}
		log.Printf("GridFS: backed up %s (import.pdf)", id)
	}()

	h.worker.Enqueue(worker.Job{FlipbookID: id, SourcePath: srcPath})

	w.WriteHeader(http.StatusCreated)
	jsonResponse(w, map[string]interface{}{
		"id":         id,
		"title":      title,
		"slug":       slug,
		"status":     "pending",
		"status_url": h.baseURL + "/api/flipbooks/" + id + "/status",
	})
}

func (h *APIHandler) DeleteFlipbook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	fb, _ := h.db.GetFlipbook(id)
	h.db.DeleteFlipbook(id)
	h.storage.DeleteFlipbook(id)
	if fb != nil && fb.GridFSFileID != "" {
		go func() {
			if err := h.db.DeleteFromGridFS(context.Background(), fb.GridFSFileID); err != nil {
				log.Printf("GridFS: cleanup failed for %s: %v", id, err)
			}
		}()
	}
	w.WriteHeader(http.StatusNoContent)
}

func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
