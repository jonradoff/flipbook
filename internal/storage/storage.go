package storage

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Storage struct {
	BaseDir string
}

func New(baseDir string) *Storage {
	os.MkdirAll(baseDir, 0755)
	return &Storage{BaseDir: baseDir}
}

func (s *Storage) FlipbookDir(id string) string {
	return filepath.Join(s.BaseDir, id)
}

func (s *Storage) PagesDir(id string) string {
	return filepath.Join(s.BaseDir, id, "pages")
}

func (s *Storage) ThumbsDir(id string) string {
	return filepath.Join(s.BaseDir, id, "thumbs")
}

func (s *Storage) OriginalPath(id, ext string) string {
	return filepath.Join(s.BaseDir, id, "original"+ext)
}

func (s *Storage) PDFPath(id string) string {
	return filepath.Join(s.BaseDir, id, "converted.pdf")
}

func (s *Storage) SaveUpload(id, filename string, r io.Reader) (string, error) {
	dir := s.FlipbookDir(id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create flipbook dir: %w", err)
	}

	ext := filepath.Ext(filename)
	dst := s.OriginalPath(id, ext)

	f, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return dst, nil
}

func (s *Storage) EnsureDirs(id string) error {
	if err := os.MkdirAll(s.PagesDir(id), 0755); err != nil {
		return err
	}
	return os.MkdirAll(s.ThumbsDir(id), 0755)
}

func (s *Storage) DeleteFlipbook(id string) error {
	return os.RemoveAll(s.FlipbookDir(id))
}

// DetectPageFormat checks the actual filenames on disk to determine
// pdftoppm's naming pattern (page-1.png vs page-01.png vs page-001.png).
func (s *Storage) DetectPageFormat(id string) string {
	pagesDir := s.PagesDir(id)
	// Check common patterns in order of likelihood
	for _, pattern := range []string{"page-1.png", "page-01.png", "page-001.png", "page-0001.png"} {
		if _, err := os.Stat(filepath.Join(pagesDir, pattern)); err == nil {
			switch pattern {
			case "page-1.png":
				return "%d"
			case "page-01.png":
				return "%02d"
			case "page-001.png":
				return "%03d"
			case "page-0001.png":
				return "%04d"
			}
		}
	}
	return "%d" // fallback
}

func (s *Storage) PageImageURL(id, format string, pageNum int) string {
	return fmt.Sprintf("/data/flipbooks/%s/pages/page-"+format+".png", id, pageNum)
}

func (s *Storage) ThumbImageURL(id, format string, pageNum int) string {
	return fmt.Sprintf("/data/flipbooks/%s/thumbs/page-"+format+".png", id, pageNum)
}

// HasPages checks if any converted page images exist on disk.
func (s *Storage) HasPages(id string) bool {
	matches, _ := filepath.Glob(filepath.Join(s.PagesDir(id), "page-*.png"))
	return len(matches) > 0
}

// LoadPageTexts reads the extracted text.json for a flipbook.
// Returns nil (not an error) if the file doesn't exist.
func (s *Storage) LoadPageTexts(id string) []string {
	data, err := os.ReadFile(filepath.Join(s.FlipbookDir(id), "text.json"))
	if err != nil {
		return nil
	}
	var texts []string
	if err := json.Unmarshal(data, &texts); err != nil {
		return nil
	}
	return texts
}
