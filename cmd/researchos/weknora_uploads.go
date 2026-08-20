package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultWeKnoraUploadMaxBytes int64 = 50 << 20

var supportedResearchUploadExtensions = map[string]struct{}{
	".pdf": {}, ".doc": {}, ".docx": {}, ".ppt": {}, ".pptx": {},
}

type researchUpload struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	FileType     string    `json:"file_type"`
	Size         int64     `json:"size"`
	CreatedAt    time.Time `json:"created_at"`
	ParseStatus  string    `json:"parse_status"`
	ErrorMessage string    `json:"error_message,omitempty"`
}

type weKnoraKnowledge struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	Title        string    `json:"title"`
	FileName     string    `json:"file_name"`
	FileType     string    `json:"file_type"`
	FileSize     int64     `json:"file_size"`
	CreatedAt    time.Time `json:"created_at"`
	ParseStatus  string    `json:"parse_status"`
	ErrorMessage string    `json:"error_message"`
}

type weKnoraKnowledgeResponse struct {
	Success bool             `json:"success"`
	Message string           `json:"message"`
	Data    weKnoraKnowledge `json:"data"`
}

type weKnoraKnowledgeListResponse struct {
	Success bool               `json:"success"`
	Message string             `json:"message"`
	Data    []weKnoraKnowledge `json:"data"`
	Page    int                `json:"page"`
	Total   int                `json:"total"`
}

type weKnoraHTTPError struct {
	StatusCode int
	Detail     string
}

func (e *weKnoraHTTPError) Error() string {
	return fmt.Sprintf("WeKnora returned HTTP %d: %s", e.StatusCode, e.Detail)
}

func loadWeKnoraUploadMaxBytes() int64 {
	raw := strings.TrimSpace(os.Getenv("RAG_UI_UPLOAD_MAX_MB"))
	if raw == "" {
		return defaultWeKnoraUploadMaxBytes
	}
	megabytes, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || megabytes < 1 || megabytes > 1024 {
		return defaultWeKnoraUploadMaxBytes
	}
	return megabytes << 20
}

func (c WeKnoraConfig) uploadsEnabled() bool {
	return c.BaseURL != "" && c.Email != "" && c.Password != "" && c.KnowledgeBaseID != ""
}

func (c WeKnoraConfig) consoleURL() string {
	if c.ConsoleURL != "" {
		return c.ConsoleURL
	}
	return c.BaseURL
}

func normalizeResearchUpload(knowledge weKnoraKnowledge) researchUpload {
	return researchUpload{
		ID:           knowledge.ID,
		Name:         firstNonEmpty(knowledge.FileName, knowledge.Title, "未命名文件"),
		FileType:     strings.ToUpper(knowledge.FileType),
		Size:         knowledge.FileSize,
		CreatedAt:    knowledge.CreatedAt,
		ParseStatus:  knowledge.ParseStatus,
		ErrorMessage: knowledge.ErrorMessage,
	}
}

func (c *WeKnoraClient) ListResearchUploads(ctx context.Context) ([]researchUpload, error) {
	if !c.config.uploadsEnabled() {
		return nil, errWeKnoraUploadsNotConfigured
	}
	login, err := c.login(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]researchUpload, 0, 10)
	for page := 1; len(items) < 10; page++ {
		path := "/api/v1/knowledge-bases/" + url.PathEscape(c.config.KnowledgeBaseID) + "/knowledge?page=" + strconv.Itoa(page) + "&page_size=50"
		var response weKnoraKnowledgeListResponse
		if err := c.json(ctx, http.MethodGet, path, &login, nil, &response); err != nil {
			return nil, err
		}
		for _, knowledge := range response.Data {
			if knowledge.Type == "file" {
				items = append(items, normalizeResearchUpload(knowledge))
			}
		}
		if len(response.Data) == 0 || len(response.Data) < 50 || page*50 >= response.Total {
			break
		}
	}
	sort.SliceStable(items, func(left, right int) bool { return items[left].CreatedAt.After(items[right].CreatedAt) })
	if len(items) > 10 {
		items = items[:10]
	}
	return items, nil
}

func (c *WeKnoraClient) UploadResearchFile(ctx context.Context, filename string, source io.Reader) (researchUpload, error) {
	if !c.config.uploadsEnabled() {
		return researchUpload{}, errWeKnoraUploadsNotConfigured
	}
	filename = filepath.Base(strings.TrimSpace(filename))
	extension := strings.ToLower(filepath.Ext(filename))
	if filename == "." || filename == "" || extension == "" {
		return researchUpload{}, errors.New("文件名无效")
	}
	if _, ok := supportedResearchUploadExtensions[extension]; !ok {
		return researchUpload{}, errors.New("仅支持 PDF、Word 或 PPT 文件")
	}
	login, err := c.login(ctx)
	if err != nil {
		return researchUpload{}, err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return researchUpload{}, err
	}
	if _, err := io.Copy(part, source); err != nil {
		return researchUpload{}, err
	}
	if err := writer.WriteField("channel", "rag-ui"); err != nil {
		return researchUpload{}, err
	}
	if err := writer.Close(); err != nil {
		return researchUpload{}, err
	}
	path := "/api/v1/knowledge-bases/" + url.PathEscape(c.config.KnowledgeBaseID) + "/knowledge/file"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL+path, &body)
	if err != nil {
		return researchUpload{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	c.authorize(request, login)
	response, err := c.http.Do(request)
	if err != nil {
		return researchUpload{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		problem, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return researchUpload{}, &weKnoraHTTPError{StatusCode: response.StatusCode, Detail: strings.TrimSpace(string(problem))}
	}
	var result weKnoraKnowledgeResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return researchUpload{}, err
	}
	if !result.Success || result.Data.ID == "" {
		return researchUpload{}, fmt.Errorf("WeKnora 未返回有效的文件记录：%s", result.Message)
	}
	return normalizeResearchUpload(result.Data), nil
}

var errWeKnoraUploadsNotConfigured = errors.New("WeKnora uploads are not configured")

func (c *WeKnoraClient) serveResearchUploads(w http.ResponseWriter, r *http.Request) {
	uploads, err := c.ListResearchUploads(r.Context())
	if err != nil {
		writeResearchUploadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"uploads": uploads, "console_url": c.config.consoleURL()})
}

func (c *WeKnoraClient) serveResearchUpload(w http.ResponseWriter, r *http.Request) {
	if !c.config.uploadsEnabled() {
		writeResearchUploadError(w, errWeKnoraUploadsNotConfigured)
		return
	}
	maxBytes := c.config.UploadMaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultWeKnoraUploadMaxBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "上传文件无效或超过大小限制"})
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请选择要上传的文件"})
		return
	}
	defer file.Close()
	if header.Size > maxBytes {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("文件大小不能超过 %d MB", maxBytes>>20)})
		return
	}
	upload, err := c.UploadResearchFile(r.Context(), header.Filename, file)
	if err != nil {
		writeResearchUploadError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]researchUpload{"upload": upload})
}

func writeResearchUploadError(w http.ResponseWriter, err error) {
	if errors.Is(err, errWeKnoraUploadsNotConfigured) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "WeKnora 知识库上传尚未配置"})
		return
	}
	var upstream *weKnoraHTTPError
	if errors.As(err, &upstream) {
		switch upstream.StatusCode {
		case http.StatusConflict:
			writeJSON(w, http.StatusConflict, map[string]string{"error": "该文件已在 WeKnora 知识库中，无需重复上传"})
		case http.StatusBadRequest:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "WeKnora 拒绝了该文件，请检查文件类型和大小"})
		default:
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "WeKnora 知识库暂时无法处理请求"})
		}
		return
	}
	if strings.Contains(err.Error(), "仅支持") || strings.Contains(err.Error(), "文件名无效") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "无法连接 WeKnora 知识库，请稍后重试"})
}
