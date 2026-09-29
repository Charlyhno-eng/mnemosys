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
	"strings"
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
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte("# Home")) || !bytes.Contains(response.Body.Bytes(), []byte(`"id":`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"revision":`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"pageType":"technical"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"owner":"Human"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"updatedAt":`)) {
		t.Fatalf("get status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestEditConflictDoesNotLeakContentWithoutViewPermission(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "private.md", Type: "document", Content: "initial"}); err != nil {
		t.Fatal(err)
	}
	document, err := service.Get("private.md")
	if err != nil {
		t.Fatal(err)
	}
	enabled := strings.Replace(document.Content, "ai_editable: false", "ai_editable: true", 1)
	if err := service.Update(UpdateInput{Path: "private.md", Content: &enabled, BaseRevision: document.Revision}); err != nil {
		t.Fatal(err)
	}
	stale, err := service.Get("private.md")
	if err != nil {
		t.Fatal(err)
	}
	secret := "latest secret"
	if err := service.Update(UpdateInput{Path: "private.md", Content: &secret, BaseRevision: stale.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfigureApplication(ApplicationSettingsInput{Profile: ProfileSettings{Type: ProfileAI}, AIPermissions: Permissions{Edit: true}}); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(UpdateInput{Path: "private.md", Content: stringPointer("stale edit"), BaseRevision: stale.Revision})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/documents", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	NewHandler(service).ServeHTTP(response, request)
	if response.Code != http.StatusConflict || bytes.Contains(response.Body.Bytes(), []byte("currentDocument")) || bytes.Contains(response.Body.Bytes(), []byte("latest secret")) {
		t.Fatalf("private conflict status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHandlerRequiresRevisionAndReturnsConcurrentVersion(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "shared.md", Type: "document", Content: "initial"}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(service)

	missing := httptest.NewRequest(http.MethodPut, "/api/documents", bytes.NewBufferString(`{"path":"shared.md","content":"missing revision"}`))
	missing.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, missing)
	if response.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing revision status = %d, body = %s", response.Code, response.Body.String())
	}

	opened, err := service.Get("shared.md")
	if err != nil {
		t.Fatal(err)
	}
	first := "first editor"
	if err := service.Update(UpdateInput{Path: "shared.md", Content: &first, BaseRevision: opened.Revision}); err != nil {
		t.Fatal(err)
	}
	staleBody, err := json.Marshal(UpdateInput{Path: "shared.md", Content: stringPointer("second editor"), BaseRevision: opened.Revision})
	if err != nil {
		t.Fatal(err)
	}
	stale := httptest.NewRequest(http.MethodPut, "/api/documents", bytes.NewReader(staleBody))
	stale.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, stale)
	if response.Code != http.StatusConflict || !bytes.Contains(response.Body.Bytes(), []byte(`"currentDocument"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`first editor`)) {
		t.Fatalf("conflict status = %d, body = %s", response.Code, response.Body.String())
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

func TestHandlerReturnsHybridSearchResults(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "runbook.md", Type: "document", Content: "Deployment runbook and recovery steps."}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/documents/search?q=deployment&mode=hybrid", nil)
	NewHandler(service).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"mode":"hybrid"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"path":"runbook.md"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"lexical"`)) {
		t.Fatalf("search status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHandlerReturnsNameSearchResults(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "handbook", Type: "directory"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Create(CreateInput{Path: "handbook/onboarding.md", Type: "document", Content: "New employee steps."}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/documents/search?q=onboarding&mode=names", nil)
	NewHandler(service).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"mode":"names"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"path":"handbook/onboarding.md"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"name"`)) {
		t.Fatalf("name search status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHandlerSearchesFrontmatterField(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "metadata.md", Type: "document", Content: "# Page"}); err != nil {
		t.Fatal(err)
	}
	document, err := service.Get("metadata.md")
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(document.Content, `team: ""`, `team: "Robotic"`, 1)
	if err := service.Update(UpdateInput{Path: "metadata.md", Content: &updated, BaseRevision: document.Revision}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/documents/search?q="+url.QueryEscape("team: Robotic")+"&mode=lexical", nil)
	NewHandler(service).ServeHTTP(response, request)
	var result SearchResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Results) != 1 || result.Results[0].Path != "metadata.md" || result.Results[0].Snippet != "team: Robotic" {
		t.Fatalf("metadata search status = %d, body = %s", response.Code, response.Body.String())
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
	body, err := json.Marshal(ApplicationSettingsInput{Profile: ProfileSettings{Type: ProfileHuman, FirstName: "Grace", LastName: "Hopper"}, AIPermissions: Permissions{View: true}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/settings/application", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	NewHandler(service).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"id":"business"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"type":"human"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"firstName":"Grace"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"view":true`)) {
		t.Fatalf("update settings status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHandlerDoesNotAllowPageTypeConfiguration(t *testing.T) {
	service, _ := newTestService(t)
	request := httptest.NewRequest(http.MethodPut, "/api/settings/application", bytes.NewBufferString(`{"profile":{"type":"human","firstName":"","lastName":""},"aiPermissions":{"view":true,"create":false,"edit":false,"delete":false},"pageTypes":[]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	NewHandler(service).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHandlerEnforcesAIReadOnlyPermissions(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "readable.md", Type: "document"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfigureApplication(ApplicationSettingsInput{Profile: ProfileSettings{Type: ProfileAI}, AIPermissions: defaultAIPermissions()}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(service)

	request := httptest.NewRequest(http.MethodPost, "/api/documents", bytes.NewBufferString(`{"path":"blocked.md","type":"document","content":""}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/documents/content?path=readable.md", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("view status = %d, body = %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/documents?path=readable.md", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("delete status = %d, body = %s", response.Code, response.Body.String())
	}

	settingsBody, err := json.Marshal(ApplicationSettingsInput{Profile: ProfileSettings{Type: ProfileAI}, AIPermissions: Permissions{View: true, Create: true}})
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPut, "/api/settings/application", bytes.NewReader(settingsBody))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("permission update status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHandlerRoutesAIChangesThroughProposalReview(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "proposal.md", Type: "document", Content: "# Current"}); err != nil {
		t.Fatal(err)
	}
	document, err := service.Get("proposal.md")
	if err != nil {
		t.Fatal(err)
	}
	enabled := strings.Replace(document.Content, "ai_editable: false", "ai_editable: true", 1)
	if err := service.Update(UpdateInput{Path: "proposal.md", Content: &enabled, BaseRevision: document.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfigureApplication(ApplicationSettingsInput{Profile: ProfileSettings{Type: ProfileAI}, AIPermissions: Permissions{View: true, Edit: true}}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(service)
	document, err = service.Get("proposal.md")
	if err != nil {
		t.Fatal(err)
	}
	proposalBody, err := json.Marshal(UpdateInput{Path: "proposal.md", Content: stringPointer("# Proposed"), BaseRevision: document.Revision})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/documents", bytes.NewReader(proposalBody))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"proposal"`)) {
		t.Fatalf("proposal update status = %d, body = %s", response.Code, response.Body.String())
	}
	var proposals []DocumentProposal
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/documents/proposals", nil))
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &proposals) != nil || len(proposals) != 1 {
		t.Fatalf("proposal list status = %d, body = %s", response.Code, response.Body.String())
	}
	if proposals[0].Status != ProposalInReview {
		t.Fatalf("proposal status = %q, want %q", proposals[0].Status, ProposalInReview)
	}
	status := httptest.NewRequest(http.MethodPatch, "/api/documents/proposals/"+proposals[0].ID, bytes.NewBufferString(`{"status":"approved"}`))
	status.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, status)
	if response.Code != http.StatusNoContent {
		t.Fatalf("proposal status update = %d, body = %s", response.Code, response.Body.String())
	}
	// Review is a human action, not an agent's self-approval.
	service.mu.Lock()
	service.profile = ProfileSettings{Type: ProfileHuman}
	service.mu.Unlock()
	accept := httptest.NewRequest(http.MethodPost, "/api/documents/proposals/"+proposals[0].ID+"/accept", bytes.NewBufferString(`{}`))
	accept.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, accept)
	if response.Code != http.StatusNoContent {
		t.Fatalf("proposal accept status = %d, body = %s", response.Code, response.Body.String())
	}
	document, err = service.Get("proposal.md")
	if err != nil || !strings.HasSuffix(document.Content, "\n\n# Proposed") {
		t.Fatalf("accepted document = %#v, %v", document, err)
	}
}

func stringPointer(value string) *string { return &value }
