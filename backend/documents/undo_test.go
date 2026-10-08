package documents

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestCreationUndoLifecycle(t *testing.T) {
	s, root := newTestService(t)
	folder, err := s.CreateWithUndo(CreateInput{Path: "folder", Type: "directory"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.CreateWithUndo(CreateInput{Path: "folder/page.md", Type: "document", Content: "initial"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UndoCreate(folder); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("nonempty folder: %v", err)
	}
	if err := s.UndoCreate(page); err != nil {
		t.Fatal(err)
	}
	if err := s.UndoCreate(page); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replay: %v", err)
	}
	if err := s.UndoCreate(folder); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "folder")); !os.IsNotExist(err) {
		t.Fatalf("folder still exists: %v", err)
	}
	if err := s.UndoCreate("../invalid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid token: %v", err)
	}
}

func TestCreationUndoPreservesChangedData(t *testing.T) {
	s, root := newTestService(t)
	token, err := s.CreateWithUndo(CreateInput{Path: "page.md", Type: "document", Content: "initial"})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.Get("page.md")
	if err != nil {
		t.Fatal(err)
	}
	changed := "changed"
	if err := s.Update(UpdateInput{Path: "page.md", Content: &changed, BaseRevision: doc.Revision}); err != nil {
		t.Fatal(err)
	}
	if err := s.UndoCreate(token); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("changed page: %v", err)
	}
	if doc, err := s.Get("page.md"); err != nil || !bytes.Contains([]byte(doc.Content), []byte(changed)) {
		t.Fatalf("lost content: %v", err)
	}
	// A restarted service cannot use an earlier undo token.
	restarted, err := NewService(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.UndoCreate(token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restart: %v", err)
	}
	if _, err := s.CreateWithUndo(CreateInput{Path: "page.md", Type: "document"}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestCreationUndoAuthorizationAndConcurrentRequests(t *testing.T) {
	s, _ := newTestService(t)
	token, err := s.CreateWithUndo(CreateInput{Path: "page.md", Type: "document"})
	if err != nil {
		t.Fatal(err)
	}
	s.profile.Type = ProfileAI
	s.aiPermissions.Delete = false
	if err := s.UndoCreate(token); !errors.Is(err, ErrForbidden) {
		t.Fatalf("delete permission: %v", err)
	}
	s.profile.Type = ProfileHuman
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- s.UndoCreate(token) }()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("successful undos = %d", success)
	}
}

func TestCreationUndoRejectsReplacedEntriesAndContextChanges(t *testing.T) {
	s, root := newTestService(t)
	token, err := s.CreateWithUndo(CreateInput{Path: "folder", Type: "directory"})
	if err != nil {
		t.Fatal(err)
	}
	s.activeProfileID = "another-profile"
	if err := s.UndoCreate(token); !errors.Is(err, ErrForbidden) {
		t.Fatalf("different profile: %v", err)
	}
	s.activeProfileID = "legacy"
	originalRoot := s.repository.root
	s.repository.root = t.TempDir()
	if err := s.UndoCreate(token); !errors.Is(err, ErrForbidden) {
		t.Fatalf("different vault: %v", err)
	}
	s.repository.root = originalRoot
	if err := os.Rename(filepath.Join(root, "folder"), filepath.Join(root, "original")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.UndoCreate(token); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("replacement: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "folder")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "original"), filepath.Join(root, "folder")); err != nil {
		t.Fatal(err)
	}
	if err := s.UndoCreate(token); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "original")); err != nil {
		t.Fatalf("original lost: %v", err)
	}
}

func TestCreationUndoHTTP(t *testing.T) {
	s, _ := newTestService(t)
	handler := NewHandler(s)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/documents", bytes.NewBufferString(`{"path":"folder","type":"directory"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	var result struct {
		UndoToken string `json:"undoToken"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusCreated || result.UndoToken == "" {
		t.Fatalf("create: %s", response.Body.String())
	}
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"token":"` + result.UndoToken + `"}`, http.StatusNoContent},
		{`{"token":"` + result.UndoToken + `"}`, http.StatusNotFound},
		{`{"token":42}`, http.StatusBadRequest},
		{`{"token":"missing","extra":true}`, http.StatusBadRequest},
	} {
		response = httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/documents/undo-create", bytes.NewBufferString(tc.body))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("%s: status %d, body %s", tc.body, response.Code, response.Body.String())
		}
	}
}
