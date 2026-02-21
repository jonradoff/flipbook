package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Port           string `yaml:"port"`
	BaseURL        string `yaml:"base_url"`
	DataDir        string `yaml:"data_dir"`
	MongoURI       string `yaml:"mongo_uri"`
	MongoDB        string `yaml:"mongo_db"`
	LibreOfficeBin string `yaml:"libreoffice_bin"`
	MaxUploadSize  int64  `yaml:"max_upload_size"`
	ConversionDPI  int    `yaml:"conversion_dpi"`
	ThumbnailDPI   int    `yaml:"thumbnail_dpi"`
	SessionSecret  string `yaml:"session_secret"`
	APIKey         string `yaml:"api_key"`
}

func Load() *Config {
	cfg := &Config{
		Port:          "8080",
		BaseURL:       "http://localhost:8080",
		DataDir:       "./data",
		MongoURI:      "",
		MongoDB:       "flipbook",
		MaxUploadSize: 104857600, // 100MB
		ConversionDPI: 300,
		ThumbnailDPI:  72,
	}

	// Try loading config file: FLIPBOOK_CONFIG env, then config.dev.yaml, then config.yaml
	configPaths := []string{
		os.Getenv("FLIPBOOK_CONFIG"),
		"config.dev.yaml",
		"config.yaml",
	}
	for _, path := range configPaths {
		if path == "" {
			continue
		}
		if data, err := os.ReadFile(path); err == nil {
			yaml.Unmarshal(data, cfg)
			break
		}
	}

	// Environment variables override config file values
	if v := os.Getenv("FLIPBOOK_PORT"); v != "" {
		cfg.Port = v
	}
	if v := os.Getenv("FLIPBOOK_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("FLIPBOOK_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("FLIPBOOK_MONGO_URI"); v != "" {
		cfg.MongoURI = v
	}
	if v := os.Getenv("FLIPBOOK_MONGO_DB"); v != "" {
		cfg.MongoDB = v
	}
	if v := os.Getenv("FLIPBOOK_LIBREOFFICE_BIN"); v != "" {
		cfg.LibreOfficeBin = v
	}
	if v := os.Getenv("FLIPBOOK_MAX_UPLOAD_SIZE"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.MaxUploadSize = n
		}
	}
	if v := os.Getenv("FLIPBOOK_CONVERSION_DPI"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.ConversionDPI = n
		}
	}
	if v := os.Getenv("FLIPBOOK_THUMBNAIL_DPI"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.ThumbnailDPI = n
		}
	}
	if v := os.Getenv("FLIPBOOK_SESSION_SECRET"); v != "" {
		cfg.SessionSecret = v
	}
	if v := os.Getenv("FLIPBOOK_API_KEY"); v != "" {
		cfg.APIKey = v
	}

	if cfg.LibreOfficeBin == "" {
		cfg.LibreOfficeBin = findLibreOffice()
	}

	// Generate a random session secret if not set
	if cfg.SessionSecret == "" {
		b := make([]byte, 32)
		rand.Read(b)
		cfg.SessionSecret = hex.EncodeToString(b)
	}

	// Generate a random API key if not set
	if cfg.APIKey == "" {
		b := make([]byte, 32)
		rand.Read(b)
		cfg.APIKey = hex.EncodeToString(b)
	}

	return cfg
}

func findLibreOffice() string {
	paths := []string{
		"/Applications/LibreOffice.app/Contents/MacOS/soffice",
		"/usr/bin/soffice",
		"/usr/local/bin/soffice",
		"/snap/bin/soffice",
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "soffice"
}
