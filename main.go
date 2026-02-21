package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jonradoff/flipbook/internal/auth"
	"github.com/jonradoff/flipbook/internal/config"
	"github.com/jonradoff/flipbook/internal/converter"
	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/handlers"
	"github.com/jonradoff/flipbook/internal/mcp"
	"github.com/jonradoff/flipbook/internal/storage"
	"github.com/jonradoff/flipbook/internal/worker"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	// Handle CLI subcommands
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "set-password":
			runSetPassword()
			return
		case "mcp":
			mcp.Run()
			return
		case "help":
			printHelp()
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
			printHelp()
			os.Exit(1)
		}
	}

	runServer()
}

func printHelp() {
	fmt.Println("Usage: flipbook [command]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  (no command)    Start the web server")
	fmt.Println("  set-password    Set the admin password")
	fmt.Println("  mcp             Start the MCP server (stdin/stdout)")
	fmt.Println("  help            Show this help message")
}

func runSetPassword() {
	cfg := config.Load()

	db, err := database.Open(context.Background(), cfg.MongoURI, cfg.MongoDB)
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer db.Close(context.Background())

	fmt.Print("Enter new admin password: ")
	var password string
	fmt.Scanln(&password)

	if len(password) < 8 {
		fmt.Println("Error: Password must be at least 8 characters.")
		os.Exit(1)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		log.Fatalf("Failed to hash password: %v", err)
	}

	if err := db.SetSetting("admin_password_hash", string(hash)); err != nil {
		log.Fatalf("Failed to save password: %v", err)
	}

	fmt.Println("Admin password set successfully.")
}

func runServer() {
	cfg := config.Load()

	// Initialize database
	os.MkdirAll(cfg.DataDir, 0755)
	db, err := database.Open(context.Background(), cfg.MongoURI, cfg.MongoDB)
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer db.Close(context.Background())

	// Initialize storage
	store := storage.New(filepath.Join(cfg.DataDir, "flipbooks"))

	// Initialize converter
	conv := converter.New(cfg.LibreOfficeBin, filepath.Join(cfg.DataDir, "tmp"), cfg.ConversionDPI, cfg.ThumbnailDPI)

	// Initialize worker
	w := worker.New(db, store, conv)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// Re-queue stuck conversions
	stuck, _ := db.GetFlipbooksByStatus("converting")
	for _, fb := range stuck {
		log.Printf("Re-queuing stuck conversion: %s", fb.ID)
		db.UpdateStatus(fb.ID, "pending", "")
		ext := filepath.Ext(fb.Filename)
		w.Enqueue(worker.Job{FlipbookID: fb.ID, SourcePath: store.OriginalPath(fb.ID, ext)})
	}

	// Parse templates
	tmpl := parseTemplates()

	// Initialize auth
	a := auth.New(db, cfg.SessionSecret, tmpl)

	// Warn if no password is set
	if !a.HasPassword() {
		log.Println("WARNING: No admin password set. Run './flipbook set-password' to secure the admin area.")
	}

	// Setup handlers
	adminH := handlers.NewAdminHandler(db, store, w, tmpl, cfg.BaseURL)
	viewerH := handlers.NewViewerHandler(db, store, tmpl, cfg.BaseURL)
	embedH := handlers.NewEmbedHandler(db, store, tmpl, cfg.BaseURL)
	apiH := handlers.NewAPIHandler(db, store, w, cfg.BaseURL)

	// Setup router
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(securityHeaders)

	// Static files
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	// Serve flipbook images with caching (path-traversal safe via filepath.Base)
	r.Get("/data/flipbooks/{id}/pages/{filename}", func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		id := filepath.Base(chi.URLParam(req, "id"))
		fname := filepath.Base(chi.URLParam(req, "filename"))
		http.ServeFile(rw, req, filepath.Join(cfg.DataDir, "flipbooks", id, "pages", fname))
	})
	r.Get("/data/flipbooks/{id}/thumbs/{filename}", func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		id := filepath.Base(chi.URLParam(req, "id"))
		fname := filepath.Base(chi.URLParam(req, "filename"))
		http.ServeFile(rw, req, filepath.Join(cfg.DataDir, "flipbooks", id, "thumbs", fname))
	})

	// Auth routes (public)
	r.Get("/login", a.LoginPage)
	r.Post("/login", a.LoginSubmit)
	r.Post("/logout", a.LogoutHandler)

	// Admin routes (protected)
	r.Group(func(r chi.Router) {
		r.Use(a.RequireAuth)
		r.Get("/admin", adminH.Index)
		r.Get("/admin/upload", adminH.UploadForm)
		r.Post("/admin/upload", adminH.Upload)
		r.Post("/admin/import", adminH.ImportURL)
		r.Get("/admin/flipbooks/{id}", adminH.Detail)
		r.Post("/admin/flipbooks/{id}/delete", adminH.Delete)
		r.Post("/admin/flipbooks/{id}/settings", adminH.Settings)
	})

	// API routes (protected by API key when set)
	r.Group(func(r chi.Router) {
		r.Use(apiAuth(cfg.APIKey))
		r.Get("/api/flipbooks", apiH.ListFlipbooks)
		r.Post("/api/flipbooks", apiH.UploadFlipbook)
		r.Post("/api/flipbooks/import", apiH.ImportURL)
		r.Get("/api/flipbooks/{id}", apiH.GetFlipbook)
		r.Delete("/api/flipbooks/{id}", apiH.DeleteFlipbook)
	})

	// Status endpoint (always accessible for upload progress tracking)
	r.Get("/api/flipbooks/{id}/status", apiH.FlipbookStatus)

	// Public viewers
	r.Get("/v/{slug}", viewerH.View)
	r.Get("/embed/{slug}", embedH.Embed)

	// Root redirect
	r.Get("/", func(rw http.ResponseWriter, req *http.Request) {
		http.Redirect(rw, req, "/admin", http.StatusFound)
	})

	// Start server
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1MB
	}

	log.Printf("Flipbook server starting on :%s", cfg.Port)
	log.Printf("Admin UI:  %s/admin", cfg.BaseURL)
	log.Printf("API key:   %s", cfg.APIKey)
	log.Printf("LibreOffice: %s", cfg.LibreOfficeBin)

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		cancel()
		srv.Shutdown(context.Background())
	}()

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// securityHeaders adds standard security headers to all responses.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// apiAuth middleware protects API routes with a bearer token.
func apiAuth(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.Header.Get("Authorization")
			if token == "Bearer "+apiKey {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized"}`))
		})
	}
}

func parseTemplates() *template.Template {
	funcMap := template.FuncMap{
		"last": func(i, total int) bool {
			return i == total-1
		},
		"add": func(a, b int) int {
			return a + b
		},
	}

	// Gather all template files
	var files []string
	patterns := []string{
		"web/templates/*.html",
		"web/templates/admin/*.html",
	}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			log.Printf("Warning: glob pattern %s: %v", pattern, err)
			continue
		}
		files = append(files, matches...)
	}

	if len(files) == 0 {
		log.Fatal("No template files found")
	}

	tmpl := template.Must(template.New("").Funcs(funcMap).ParseFiles(files...))
	return tmpl
}
