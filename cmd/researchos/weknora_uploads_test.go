package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWeKnoraResearchUploadsListAndUpload(t *testing.T) {
	created := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/login":
			_, _ = io.WriteString(w, `{"success":true,"token":"test-token","active_tenant":{"id":"tenant-a"}}`)
		case "/api/v1/knowledge-bases/kb-1/knowledge":
			if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("page_size") != "50" {
				t.Fatalf("unexpected list query: %s", r.URL.RawQuery)
			}
			if r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("X-Tenant-ID") != "tenant-a" {
				t.Fatalf("knowledge list is not authorized")
			}
			data := make([]weKnoraKnowledge, 0, 12)
			for index := 0; index < 12; index++ {
				data = append(data, weKnoraKnowledge{ID: "file-" + string(rune('a'+index)), Type: "file", FileName: "report.pdf", FileType: "pdf", FileSize: int64(index + 1), CreatedAt: created.Add(time.Duration(index) * time.Minute), ParseStatus: "completed"})
			}
			_ = json.NewEncoder(w).Encode(weKnoraKnowledgeListResponse{Success: true, Data: data, Page: 1, Total: 12})
		case "/api/v1/knowledge-bases/kb-1/knowledge/file":
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Fatalf("upload is not authorized")
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("parse multipart: %v", err)
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Fatalf("missing file: %v", err)
			}
			defer file.Close()
			contents, _ := io.ReadAll(file)
			if header.Filename != "research.pdf" || string(contents) != "pdf-content" || r.FormValue("channel") != "rag-ui" {
				t.Fatalf("unexpected multipart upload: file=%s body=%q channel=%q", header.Filename, contents, r.FormValue("channel"))
			}
			_ = json.NewEncoder(w).Encode(weKnoraKnowledgeResponse{Success: true, Data: weKnoraKnowledge{ID: "file-new", Type: "file", FileName: "research.pdf", FileType: "pdf", FileSize: 11, CreatedAt: created, ParseStatus: "processing"}})
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewWeKnoraClient(WeKnoraConfig{BaseURL: server.URL, ConsoleURL: "https://console.example", Email: "researcher@example.com", Password: "super-secret", KnowledgeBaseID: "kb-1", UploadMaxBytes: defaultWeKnoraUploadMaxBytes})
	uploads, err := client.ListResearchUploads(context.Background())
	if err != nil {
		t.Fatalf("ListResearchUploads() error = %v", err)
	}
	if len(uploads) != 10 || uploads[0].ID != "file-l" || uploads[9].ID != "file-c" {
		t.Fatalf("expected latest ten uploads, got %+v", uploads)
	}
	upload, err := client.UploadResearchFile(context.Background(), "research.pdf", strings.NewReader("pdf-content"))
	if err != nil || upload.ID != "file-new" || upload.ParseStatus != "processing" {
		t.Fatalf("UploadResearchFile() = %+v, %v", upload, err)
	}
	encoded, err := json.Marshal(map[string]any{"uploads": uploads, "console_url": client.config.consoleURL()})
	if err != nil || strings.Contains(string(encoded), "super-secret") || strings.Contains(string(encoded), "test-token") || strings.Contains(string(encoded), "kb-1") {
		t.Fatalf("upload response leaked configuration: %s", encoded)
	}
}

func TestServeResearchUploadValidationAndErrors(t *testing.T) {
	client := NewWeKnoraClient(WeKnoraConfig{BaseURL: "http://weknora.test", Email: "user@example.com", Password: "secret", KnowledgeBaseID: "kb-1", UploadMaxBytes: 1 << 20})

	requestBody := &bytes.Buffer{}
	writer := multipart.NewWriter(requestBody)
	part, err := writer.CreateFormFile("file", "not-a-report.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, "not supported")
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/research/uploads", requestBody)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	client.serveResearchUpload(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "仅支持") {
		t.Fatalf("unsupported file response = %d %s", response.Code, response.Body.String())
	}

	unconfigured := NewWeKnoraClient(WeKnoraConfig{})
	response = httptest.NewRecorder()
	unconfigured.serveResearchUploads(response, httptest.NewRequest(http.MethodGet, "/api/v1/research/uploads", nil))
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("unconfigured response = %d %s", response.Code, response.Body.String())
	}
}
