package converter

import (
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Result struct {
	PageCount int
	Width     int
	Height    int
}

type Converter struct {
	libreOfficeBin string
	tmpDir         string
	conversionDPI  int
	thumbnailDPI   int
}

func New(libreOfficeBin, tmpDir string, conversionDPI, thumbnailDPI int) *Converter {
	os.MkdirAll(tmpDir, 0755)
	return &Converter{
		libreOfficeBin: libreOfficeBin,
		tmpDir:         tmpDir,
		conversionDPI:  conversionDPI,
		thumbnailDPI:   thumbnailDPI,
	}
}

func (c *Converter) Convert(ctx context.Context, srcPath, pagesDir, thumbsDir string) (*Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	outDir := filepath.Dir(pagesDir)
	var pdfPath string

	ext := strings.ToLower(filepath.Ext(srcPath))
	if ext == ".pdf" {
		// Already a PDF — skip LibreOffice conversion
		pdfPath = srcPath
	} else {
		// Step 1: PPTX/PPT → PDF
		var err error
		pdfPath, err = c.pptxToPDF(ctx, srcPath, outDir)
		if err != nil {
			return nil, fmt.Errorf("pptx to pdf: %w", err)
		}
	}

	// Step 2: PDF → PNG pages (high DPI)
	pageCount, err := c.pdfToPNG(ctx, pdfPath, pagesDir, c.conversionDPI)
	if err != nil {
		return nil, fmt.Errorf("pdf to png: %w", err)
	}

	// Step 3: PDF → PNG thumbnails (low DPI)
	_, err = c.pdfToPNG(ctx, pdfPath, thumbsDir, c.thumbnailDPI)
	if err != nil {
		return nil, fmt.Errorf("pdf to thumbs: %w", err)
	}

	// Step 4: Extract text from PDF for search
	if err := c.extractText(ctx, pdfPath, outDir); err != nil {
		// Non-fatal: search just won't work for this flipbook
		fmt.Printf("[converter] Warning: text extraction failed: %v\n", err)
	}

	// Step 5: Detect dimensions from first page
	// pdftoppm names files page-1.png, page-2.png etc.
	absPagesDir, _ := filepath.Abs(pagesDir)
	firstPage := filepath.Join(absPagesDir, "page-1.png")
	width, height, err := detectDimensions(firstPage)
	if err != nil {
		// Try alternate naming patterns
		for _, pattern := range []string{"page-01.png", "page-001.png"} {
			width, height, err = detectDimensions(filepath.Join(absPagesDir, pattern))
			if err == nil {
				break
			}
		}
		if err != nil {
			return nil, fmt.Errorf("detect dimensions: %w", err)
		}
	}

	return &Result{
		PageCount: pageCount,
		Width:     width,
		Height:    height,
	}, nil
}

func (c *Converter) extractText(ctx context.Context, pdfPath, outDir string) error {
	absPDF, err := filepath.Abs(pdfPath)
	if err != nil {
		return err
	}

	// Run pdftotext to get all text, pages separated by form feed
	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", absPDF, "-")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("pdftotext: %w", err)
	}

	// Split by form feed character (page separator)
	rawPages := strings.Split(string(out), "\f")

	// Trim and collect non-empty page text
	var pageTexts []string
	for _, p := range rawPages {
		trimmed := strings.TrimSpace(p)
		pageTexts = append(pageTexts, trimmed)
	}

	// pdftotext appends a trailing form feed, remove empty last entry
	if len(pageTexts) > 0 && pageTexts[len(pageTexts)-1] == "" {
		pageTexts = pageTexts[:len(pageTexts)-1]
	}

	// Write as JSON
	data, err := json.Marshal(pageTexts)
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(outDir, "text.json"), data, 0644)
}

func detectDimensions(imagePath string) (int, int, error) {
	f, err := os.Open(imagePath)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	cfg, err := png.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}
