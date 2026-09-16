package documents

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

var tinyPNG = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0, 'I', 'H', 'D', 'R'}

func TestHandlerDocumentEndpoints(t *testing.T) {
	service, _ := newTestService(t)
	handler := NewHandler(service)

	create := httptest.NewRequest(http.MethodPost, "/api/documents", bytes.NewBufferString(`{"path":"home.md","type":"document","pageType":"technical","content":"# Home"}`))
	create.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, create)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/api/documents/content?path=home.md", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, get)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte("# Home")) || !bytes.Contains(response.Body.Bytes(), []byte(`"id":`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"pageType":"technical"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"updatedAt":`)) {
		t.Fatalf("get status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHandlerRejectsInvalidPageType(t *testing.T) {
	service, _ := newTestService(t)
	request := httptest.NewRequest(http.MethodPost, "/api/documents", bytes.NewBufferString(`{"path":"home.md","type":"document","pageType":"secret"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	NewHandler(service).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHandlerReturnsDocumentGraph(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "docs", Type: "directory"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Create(CreateInput{Path: "docs/home.md", Type: "document"}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	NewHandler(service).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/documents/graph", nil))
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"type":"hierarchy"`)) {
		t.Fatalf("graph status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHandlerRejectsUnknownJSONFields(t *testing.T) {
	service, _ := newTestService(t)
	handler := NewHandler(service)
	request := httptest.NewRequest(http.MethodPost, "/api/documents", bytes.NewBufferString(`{"path":"home.md","type":"document","unexpected":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHandlerMoveReturnsDestination(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "before.md", Type: "document"}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(service)
	request := httptest.NewRequest(http.MethodPut, "/api/documents", bytes.NewBufferString(`{"path":"before.md","newPath":"after.md"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"path":"after.md"`)) {
		t.Fatalf("move status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHandlerUploadsAndServesImage(t *testing.T) {
	service, _ := newTestService(t)
	handler := NewHandler(service)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "diagram.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(tinyPNG); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/assets", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", response.Code, response.Body.String())
	}
	var result struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, result.URL, nil))
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), tinyPNG) {
		t.Fatalf("asset response status = %d, body = %v", response.Code, response.Body.Bytes())
	}
}

func TestHandlerRejectsNonImageAsset(t *testing.T) {
	service, _ := newTestService(t)
	handler := NewHandler(service)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "notes.txt")
	_, _ = part.Write([]byte("not an image"))
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/assets", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHandlerConfiguresStorage(t *testing.T) {
	service, _ := newTestService(t)
	handler := NewHandler(service)
	target := filepath.Join(t.TempDir(), "knowledge")
	body := bytes.NewBufferString(`{"path":` + strconv.Quote(target) + `}`)
	request := httptest.NewRequest(http.MethodPut, "/api/settings/storage", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"configured":true`)) {
		t.Fatalf("configure status = %d, body = %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/settings/storage", nil))
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(target)) {
		t.Fatalf("settings status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHandlerBrowsesDirectories(t *testing.T) {
	service, _ := newTestService(t)
	handler := NewHandler(service)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/settings/directories?path="+url.QueryEscape(root), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"name":"docs"`)) {
		t.Fatalf("browse status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHandlerUpdatesApplicationSettings(t *testing.T) {
	service, _ := newTestService(t)
	body, err := json.Marshal(ApplicationSettingsInput{PageTypes: defaultPageTypes()})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/settings/application", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	NewHandler(service).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"id":"business"`)) {
		t.Fatalf("update settings status = %d, body = %s", response.Code, response.Body.String())
	}
}
