package documents

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfilesStartEmptyAndPersistIndependently(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.toml")
	service, err := NewConfigurableService(filepath.Join(root, "vault"), configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := service.ApplicationSettings(); len(got.Profiles) != 0 || got.ActiveProfileID != "" {
		t.Fatalf("unexpected initial profiles: %#v", got)
	}
	if err := service.Create(CreateInput{Path: "blocked.md", Type: "document"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unconfigured create: %v", err)
	}
	ai, err := service.CreateProfile(ProfileInput{Profile: ProfileSettings{Type: ProfileAI, Name: "Indexer", Team: "Search"}, Permissions: Permissions{View: true, Edit: true}})
	if err != nil {
		t.Fatal(err)
	}
	if ai.ActiveProfileID != "" || len(ai.Profiles) != 1 {
		t.Fatalf("AI bootstrap: %#v", ai)
	}
	human, err := service.CreateProfile(ProfileInput{Profile: ProfileSettings{Type: ProfileHuman, FirstName: "Ada", LastName: "Lovelace"}})
	if err != nil {
		t.Fatal(err)
	}
	if human.ActiveProfileID == "" || human.Profile.FirstName != "Ada" || len(human.Profiles) != 2 {
		t.Fatalf("human bootstrap: %#v", human)
	}
	if _, err := service.UpdateProfile(ai.Profiles[0].ID, ProfileInput{Profile: ProfileSettings{Type: ProfileAI, Name: "Researcher"}, Permissions: Permissions{View: true, Create: true}}); err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateProfile(ProfileInput{Profile: ProfileSettings{Type: ProfileHuman, FirstName: "Grace", LastName: "Hopper"}})
	if err != nil {
		t.Fatal(err)
	}
	secondID := second.Profiles[len(second.Profiles)-1].ID
	if _, err := service.SelectProfile(secondID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.callMCPToolForProfile(ai.Profiles[0].ID, "create", map[string]string{"path": "ai.md", "type": "document"}); err != nil {
		t.Fatalf("AI create: %v", err)
	}
	if got, err := service.Get("ai.md"); err != nil || got.Owner != "Researcher" {
		t.Fatalf("AI owner: %#v %v", got, err)
	}
	if _, err := service.callMCPToolForProfile(ai.Profiles[0].ID, "delete", map[string]string{"path": "ai.md"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("AI delete without right: %v", err)
	}
	if _, err := service.UpdateProfile(ai.Profiles[0].ID, ProfileInput{Profile: ProfileSettings{Type: ProfileAI, Name: "Researcher"}, Permissions: Permissions{View: true, Create: true, Delete: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.callMCPToolForProfile(ai.Profiles[0].ID, "delete", map[string]string{"path": "ai.md"}); err != nil {
		t.Fatalf("AI delete with right: %v", err)
	}
	if _, err := service.callMCPToolForProfile("unknown", "read", map[string]string{"path": "ai.md"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unknown AI: %v", err)
	}
	if _, err := service.callMCPToolForProfile("", "create", map[string]string{"path": "unassigned.md", "type": "document"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unassigned AI: %v", err)
	}
	if _, err := service.callMCPToolForProfile("", "read", map[string]string{"path": "ai.md"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unassigned AI read: %v", err)
	}
	blocked, err := service.CreateProfile(ProfileInput{Profile: ProfileSettings{Type: ProfileAI, Name: "Blocked"}})
	if err != nil {
		t.Fatal(err)
	}
	blockedID := blocked.Profiles[len(blocked.Profiles)-1].ID
	if _, err := service.callMCPToolForProfile(blockedID, "read", map[string]string{"path": "ai.md"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("AI without view: %v", err)
	}
	reloaded, err := NewConfigurableService(filepath.Join(root, "other"), configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.ApplicationSettings()
	if len(got.Profiles) != 4 || got.Profiles[0].Name != "Researcher" || got.Profiles[0].Permissions.Create != true || got.ActiveProfileID != secondID || got.Profile.FirstName != "Grace" {
		t.Fatalf("reloaded: %#v", got)
	}
}

func TestTrackedStarterConfigHasNoProfiles(t *testing.T) {
	data := []byte("[storage]\npath = \"\"\n[profile]\ntype = \"human\"\nfirst_name = \"\"\nlast_name = \"\"\nteam = \"\"\n[ai_permissions]\nview = true\ncreate = false\nedit = false\ndelete = false\n")
	config, err := parseApplicationConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Profiles) != 0 || config.ActiveProfileID != "" {
		t.Fatalf("starter config: %#v", config)
	}
	root := t.TempDir()
	path := filepath.Join(root, "config.toml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	service, err := NewConfigurableService(filepath.Join(root, "vault"), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Create(CreateInput{Path: "blocked.md", Type: "document"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("starter config allowed create: %v", err)
	}
}

func TestProfileValidationAndAIAuthorization(t *testing.T) {
	service, _ := newTestService(t)
	cases := []ProfileSettings{
		{Type: ProfileHuman, FirstName: "Only"},
		{Type: ProfileAI, FirstName: "Wrong", Name: "Agent"},
		{Type: ProfileAI, Name: ""},
		{Type: ProfileHuman, FirstName: "New\nline", LastName: "Name"},
	}
	for _, profile := range cases {
		if _, err := service.CreateProfile(ProfileInput{Profile: profile}); !errors.Is(err, ErrInvalidSettings) {
			t.Fatalf("profile %#v: %v", profile, err)
		}
	}
	if _, err := service.CreateProfile(ProfileInput{Profile: ProfileSettings{Type: ProfileAI, Name: "Invalid"}, Permissions: Permissions{Create: true}}); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("AI create without view: %v", err)
	}
	ai, err := service.CreateProfile(ProfileInput{Profile: ProfileSettings{Type: ProfileAI, Name: "Guard"}, Permissions: Permissions{View: true}})
	if err != nil {
		t.Fatal(err)
	}
	id := ai.Profiles[len(ai.Profiles)-1].ID
	if _, err := service.SelectProfile(id); !errors.Is(err, ErrForbidden) {
		t.Fatalf("AI selected for browser: %v", err)
	}
	if _, err := service.ConfigureApplication(ApplicationSettingsInput{Profile: ProfileSettings{Type: ProfileAI}, AIPermissions: defaultAIPermissions()}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateProfile(ProfileInput{Profile: ProfileSettings{Type: ProfileHuman, FirstName: "Eve", LastName: "Example"}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("AI created human: %v", err)
	}
	if _, err := service.UpdateProfile(id, ProfileInput{Profile: ProfileSettings{Type: ProfileAI, Name: "Guard"}, Permissions: Permissions{View: true, Delete: true}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("AI raised rights: %v", err)
	}
	if _, err := service.SelectProfile("legacy"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("AI selected human: %v", err)
	}
	if got := service.ApplicationSettings().Profiles[len(ai.Profiles)-1].Permissions; got.Delete {
		t.Fatalf("rights changed: %#v", got)
	}
}

func TestProfileHTTPAndLegacyMigration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.toml")
	legacy := `[storage]
path = ""
[profile]
type = "human"
first_name = "Grace"
last_name = "Hopper"
team = "Navy"
[ai_permissions]
view = true
create = false
edit = false
delete = false
`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	service, err := NewConfigurableService(filepath.Join(root, "vault"), path)
	if err != nil {
		t.Fatal(err)
	}
	if got := service.ApplicationSettings(); len(got.Profiles) != 1 || got.Profile.FirstName != "Grace" {
		t.Fatalf("legacy: %#v", got)
	}
	handler := NewHandler(service)
	payload, _ := json.Marshal(ProfileInput{Profile: ProfileSettings{Type: ProfileAI, Name: "Writer"}, Permissions: Permissions{View: true, Create: true}})
	request := httptest.NewRequest(http.MethodPost, "/api/profiles", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"name":"Writer"`) {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	reloaded, err := NewConfigurableService(filepath.Join(root, "vault"), path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.ApplicationSettings(); len(got.Profiles) != 2 || got.ActiveProfileID != "legacy" {
		t.Fatalf("migration: %#v", got)
	}
}

func TestMCPUsesSelectedAIProfilePermissions(t *testing.T) {
	service, _ := newTestService(t)
	settings, err := service.CreateProfile(ProfileInput{Profile: ProfileSettings{Type: ProfileAI, Name: "Writer"}, Permissions: Permissions{View: true, Create: true}})
	if err != nil {
		t.Fatal(err)
	}
	id := settings.Profiles[len(settings.Profiles)-1].ID
	call := func(profileID, path string) *httptest.ResponseRecorder {
		t.Helper()
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create","arguments":{"path":"` + path + `","type":"document"}}}`
		request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/mcp", strings.NewReader(body))
		request.RemoteAddr = "127.0.0.1:12345"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("X-Mnemosys-Profile-ID", profileID)
		response := httptest.NewRecorder()
		NewHandler(service).ServeHTTP(response, request)
		return response
	}
	denied := call("", "denied.md")
	if denied.Code != http.StatusOK || !strings.Contains(denied.Body.String(), `"isError":true`) {
		t.Fatalf("unassigned MCP: %d %s", denied.Code, denied.Body.String())
	}
	allowed := call(id, "allowed.md")
	if allowed.Code != http.StatusOK || !strings.Contains(allowed.Body.String(), `"isError":false`) {
		t.Fatalf("assigned MCP: %d %s", allowed.Code, allowed.Body.String())
	}
	if got, err := service.Get("allowed.md"); err != nil || got.Owner != "Writer" {
		t.Fatalf("created page: %#v %v", got, err)
	}
}
