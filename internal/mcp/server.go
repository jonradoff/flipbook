package mcp

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"time"

	"github.com/jonradoff/flipbook/internal/config"
)

// JSON-RPC 2.0 types

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCP types

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initResult struct {
	ProtocolVersion string            `json:"protocolVersion"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      serverInfo        `json:"serverInfo"`
}

type serverCapabilities struct {
	Tools *toolsCap `json:"tools,omitempty"`
}

type toolsCap struct{}

type tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema inputSchema `json:"inputSchema"`
}

type inputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	Required   []string               `json:"required,omitempty"`
}

type toolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type toolResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Run starts the MCP server on stdin/stdout.
func Run() {
	// Redirect log output to stderr so stdout is clean for JSON-RPC
	log.SetOutput(os.Stderr)
	log.SetPrefix("[flipbook-mcp] ")

	cfg := config.Load()
	s := &server{
		baseURL: cfg.BaseURL,
		apiKey:  cfg.APIKey,
		client:  &http.Client{Timeout: 30 * time.Second},
	}

	scanner := bufio.NewScanner(os.Stdin)
	// Allow up to 200MB for base64 file payloads
	scanner.Buffer(make([]byte, 0, 64*1024), 200*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			log.Printf("Invalid JSON: %v", err)
			continue
		}

		resp := s.handle(req)
		if resp != nil {
			data, _ := json.Marshal(resp)
			fmt.Fprintf(os.Stdout, "%s\n", data)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Scanner error: %v", err)
	}
}

type server struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func (s *server) handle(req request) *response {
	switch req.Method {
	case "initialize":
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: initResult{
				ProtocolVersion: "2024-11-05",
				Capabilities:    serverCapabilities{Tools: &toolsCap{}},
				ServerInfo:      serverInfo{Name: "flipbook", Version: "1.0.0"},
			},
		}

	case "notifications/initialized":
		// Notification, no response
		return nil

	case "tools/list":
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"tools": s.toolDefinitions(),
			},
		}

	case "tools/call":
		var params toolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.errorResp(req.ID, -32602, "Invalid params")
		}
		result := s.callTool(params)
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
		}

	case "shutdown":
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{},
		}

	default:
		return s.errorResp(req.ID, -32601, "Method not found: "+req.Method)
	}
}

func (s *server) errorResp(id json.RawMessage, code int, msg string) *response {
	return &response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg},
	}
}

func (s *server) toolDefinitions() []tool {
	return []tool{
		{
			Name:        "list_flipbooks",
			Description: "List all flipbooks with their status, viewer URL, and embed URL.",
			InputSchema: inputSchema{Type: "object"},
		},
		{
			Name:        "create_flipbook",
			Description: "Upload a file (PowerPoint or PDF) to create a new flipbook. Accepts base64-encoded file content. Waits for conversion to complete and returns the viewer/embed URLs.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"title":       map[string]string{"type": "string", "description": "Title for the flipbook (optional, defaults to filename)"},
					"filename":    map[string]string{"type": "string", "description": "Original filename with extension (e.g. presentation.pptx)"},
					"file_base64": map[string]string{"type": "string", "description": "Base64-encoded file content"},
				},
				Required: []string{"filename", "file_base64"},
			},
		},
		{
			Name:        "import_google_slides",
			Description: "Import a Google Slides presentation by URL to create a new flipbook. The presentation must be publicly shared. Waits for conversion to complete.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"title": map[string]string{"type": "string", "description": "Title for the flipbook (optional)"},
					"url":   map[string]string{"type": "string", "description": "Google Slides share URL"},
				},
				Required: []string{"url"},
			},
		},
		{
			Name:        "get_flipbook",
			Description: "Get detailed information about a specific flipbook including page URLs, dimensions, and embed code.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"id": map[string]string{"type": "string", "description": "Flipbook ID"},
				},
				Required: []string{"id"},
			},
		},
		{
			Name:        "get_flipbook_status",
			Description: "Check the conversion status of a flipbook (pending, converting, ready, or error).",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"id": map[string]string{"type": "string", "description": "Flipbook ID"},
				},
				Required: []string{"id"},
			},
		},
		{
			Name:        "delete_flipbook",
			Description: "Delete a flipbook and all its associated files.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"id": map[string]string{"type": "string", "description": "Flipbook ID"},
				},
				Required: []string{"id"},
			},
		},
	}
}

func (s *server) callTool(params toolCallParams) toolResult {
	switch params.Name {
	case "list_flipbooks":
		return s.toolListFlipbooks()
	case "create_flipbook":
		return s.toolCreateFlipbook(params.Arguments)
	case "import_google_slides":
		return s.toolImportGoogleSlides(params.Arguments)
	case "get_flipbook":
		return s.toolGetFlipbook(params.Arguments)
	case "get_flipbook_status":
		return s.toolGetFlipbookStatus(params.Arguments)
	case "delete_flipbook":
		return s.toolDeleteFlipbook(params.Arguments)
	default:
		return textError("Unknown tool: " + params.Name)
	}
}

// Tool implementations

func (s *server) toolListFlipbooks() toolResult {
	body, err := s.apiGet("/api/flipbooks")
	if err != nil {
		return textError("Failed to list flipbooks: " + err.Error())
	}
	return textResult(string(body))
}

func (s *server) toolCreateFlipbook(args map[string]interface{}) toolResult {
	filename, _ := args["filename"].(string)
	fileB64, _ := args["file_base64"].(string)
	title, _ := args["title"].(string)

	if filename == "" || fileB64 == "" {
		return textError("filename and file_base64 are required")
	}

	// Decode base64
	fileData, err := base64.StdEncoding.DecodeString(fileB64)
	if err != nil {
		return textError("Invalid base64 data: " + err.Error())
	}

	// Build multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if title != "" {
		writer.WriteField("title", title)
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return textError("Failed to create form: " + err.Error())
	}
	part.Write(fileData)
	writer.Close()

	// Upload
	req, _ := http.NewRequest("POST", s.baseURL+"/api/flipbooks", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	s.setAuth(req)

	resp, err := s.client.Do(req)
	if err != nil {
		return textError("Upload failed: " + err.Error())
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return textError("Upload failed (" + resp.Status + "): " + string(respBody))
	}

	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	id, _ := result["id"].(string)
	if id == "" {
		return textError("No flipbook ID in response")
	}

	// Poll for completion
	return s.pollUntilReady(id)
}

func (s *server) toolImportGoogleSlides(args map[string]interface{}) toolResult {
	url, _ := args["url"].(string)
	title, _ := args["title"].(string)

	if url == "" {
		return textError("url is required")
	}

	// POST to /admin/import endpoint
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	writer.WriteField("url", url)
	if title != "" {
		writer.WriteField("title", title)
	}
	writer.Close()

	req, _ := http.NewRequest("POST", s.baseURL+"/api/flipbooks/import", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	s.setAuth(req)

	resp, err := s.client.Do(req)
	if err != nil {
		return textError("Import failed: " + err.Error())
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return textError("Import failed (" + resp.Status + "): " + string(respBody))
	}

	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	id, _ := result["id"].(string)
	if id == "" {
		return textError("No flipbook ID in response")
	}

	return s.pollUntilReady(id)
}

func (s *server) toolGetFlipbook(args map[string]interface{}) toolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return textError("id is required")
	}
	body, err := s.apiGet("/api/flipbooks/" + id)
	if err != nil {
		return textError("Failed to get flipbook: " + err.Error())
	}
	return textResult(string(body))
}

func (s *server) toolGetFlipbookStatus(args map[string]interface{}) toolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return textError("id is required")
	}
	body, err := s.apiGet("/api/flipbooks/" + id + "/status")
	if err != nil {
		return textError("Failed to get status: " + err.Error())
	}
	return textResult(string(body))
}

func (s *server) toolDeleteFlipbook(args map[string]interface{}) toolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return textError("id is required")
	}

	req, _ := http.NewRequest("DELETE", s.baseURL+"/api/flipbooks/"+id, nil)
	s.setAuth(req)

	resp, err := s.client.Do(req)
	if err != nil {
		return textError("Delete failed: " + err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return textError("Delete failed: " + string(body))
	}

	return textResult("Flipbook " + id + " deleted successfully.")
}

// Helpers

func (s *server) apiGet(path string) ([]byte, error) {
	req, _ := http.NewRequest("GET", s.baseURL+path, nil)
	s.setAuth(req)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s: %s", resp.Status, string(body))
	}

	return body, nil
}

func (s *server) setAuth(req *http.Request) {
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
}

func (s *server) pollUntilReady(id string) toolResult {
	timeout := time.After(5 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return textError("Conversion timed out after 5 minutes. Check status with get_flipbook_status.")
		case <-ticker.C:
			body, err := s.apiGet("/api/flipbooks/" + id + "/status")
			if err != nil {
				log.Printf("Poll error: %v", err)
				continue
			}

			var status map[string]interface{}
			json.Unmarshal(body, &status)

			st, _ := status["status"].(string)
			switch st {
			case "ready":
				// Get full flipbook details
				details, err := s.apiGet("/api/flipbooks/" + id)
				if err != nil {
					return textResult(string(body))
				}
				return textResult(string(details))
			case "error":
				errMsg, _ := status["error"].(string)
				return textError("Conversion failed: " + errMsg)
			}
			// pending or converting — keep polling
		}
	}
}

func textResult(text string) toolResult {
	return toolResult{
		Content: []contentBlock{{Type: "text", Text: text}},
	}
}

func textError(text string) toolResult {
	return toolResult{
		Content: []contentBlock{{Type: "text", Text: text}},
		IsError: true,
	}
}
