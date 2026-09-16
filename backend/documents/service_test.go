package documents

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	service, err := NewService(root)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service, root
}

func TestDocumentLifecycle(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "engineering", Type: "directory"}); err != nil {
		t.Fatalf("create folder: %v", err)
	}
	if err := service.Create(CreateInput{Path: "engineering/guide.md", Type: "document", Content: "# Guide"}); err != nil {
		t.Fatalf("create document: %v", err)
	}
	document, err := service.Get("engineering/guide.md")
	if err != nil || !strings.Contains(document.Content, "name: \"guide\"") || !strings.HasSuffix(document.Content, "# Guide") {
		t.Fatalf("get document = %#v, %v", document, err)
	}
	content := "# Updated"
	newPath := "engineering/onboarding.md"
	if err := service.Update(UpdateInput{Path: "engineering/guide.md", NewPath: &newPath, Content: &content}); err != nil {
		t.Fatalf("update document: %v", err)
	}
	document, err = service.Get(newPath)
	if err != nil || !strings.HasSuffix(document.Content, content) {
		t.Fatalf("updated document = %#v, %v", document, err)
	}
	if err := service.Delete("engineering"); err != nil {
		t.Fatalf("delete folder: %v", err)
	}
	if _, err := service.Get(newPath); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted document error = %v, want not found", err)
	}
}

func TestDocumentsReceiveDefaultProperties(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "Product Guide.md", Type: "document", Content: "# Guide"}); err != nil {
		t.Fatal(err)
	}
	document, err := service.Get("Product Guide.md")
	if err != nil {
		t.Fatal(err)
	}
	if document.ID == "" || !frontmatterIDPattern.MatchString(document.Content) || !strings.Contains(document.Content, `name: "Product Guide"`) || !strings.Contains(document.Content, `description: ""`) || !strings.Contains(document.Content, `page_type: "general"`) || !strings.HasSuffix(document.Content, "# Guide") {
		t.Fatalf("document = %#v", document)
	}

	content := "---\nname: \"Custom\"\n---\n\nText"
	if err := service.Update(UpdateInput{Path: "Product Guide.md", Content: &content}); err != nil {
		t.Fatal(err)
	}
	document, err = service.Get("Product Guide.md")
	if err != nil || !strings.Contains(document.Content, "description: \"\"") || strings.Count(document.Content, "name:") != 1 {
		t.Fatalf("updated properties = %q, %v", document.Content, err)
	}
}

func TestDocumentIDIsImmutableAcrossWritesAndMoves(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "before.md", Type: "document", Content: "# Before"}); err != nil {
		t.Fatal(err)
	}
	created, err := service.Get("before.md")
	if err != nil || created.ID == "" {
		t.Fatalf("created document = %#v, %v", created, err)
	}
	content := "---\nid: \"00000000-0000-4000-8000-000000000000\"\n---\n\n# Changed"
	if err := service.Update(UpdateInput{Path: "before.md", Content: &content}); err != nil {
		t.Fatal(err)
	}
	destination := "after.md"
	if err := service.Update(UpdateInput{Path: "before.md", NewPath: &destination}); err != nil {
		t.Fatal(err)
	}
	moved, err := service.Get(destination)
	if err != nil || moved.ID != created.ID || !strings.Contains(moved.Content, `id: "`+created.ID+`"`) {
		t.Fatalf("moved document = %#v, original ID = %q, error = %v", moved, created.ID, err)
	}
}

func TestExistingDocumentsReceiveUniqueStableIDs(t *testing.T) {
	root := t.TempDir()
	duplicateID := "11111111-1111-4111-8111-111111111111"
	legacyDocuments := map[string]string{
		"one.md":   "---\nid: \"" + duplicateID + "\"\n---\n\n# One",
		"two.md":   "---\nid: \"" + duplicateID + "\"\n---\n\n# Two",
		"three.md": "# Missing ID",
	}
	for name, content := range legacyDocuments {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	service, err := NewService(root)
	if err != nil {
		t.Fatal(err)
	}
	one, err := service.Get("one.md")
	if err != nil {
		t.Fatal(err)
	}
	two, err := service.Get("two.md")
	if err != nil {
		t.Fatal(err)
	}
	three, err := service.Get("three.md")
	if err != nil {
		t.Fatal(err)
	}
	if one.ID == "" || two.ID == "" || three.ID == "" || one.ID == two.ID || one.ID == three.ID || two.ID == three.ID {
		t.Fatalf("backfilled IDs = %q, %q and %q", one.ID, two.ID, three.ID)
	}
	info, err := os.Stat(filepath.Join(root, "three.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("backfill changed file mode to %v", info.Mode().Perm())
	}
	reloaded, err := NewService(root)
	if err != nil {
		t.Fatal(err)
	}
	oneAgain, err := reloaded.Get("one.md")
	if err != nil || oneAgain.ID != one.ID {
		t.Fatalf("reloaded ID = %q, want %q, error = %v", oneAgain.ID, one.ID, err)
	}
}

func TestDocumentPageTypeAndUpdatedAt(t *testing.T) {
	service, root := newTestService(t)
	if err := service.Create(CreateInput{Path: "system.md", Type: "document", PageType: "technical", Content: "# System"}); err != nil {
		t.Fatal(err)
	}
	document, err := service.Get("system.md")
	if err != nil {
		t.Fatal(err)
	}
	if document.PageType != "technical" || !strings.Contains(document.Content, `page_type: "technical"`) {
		t.Fatalf("document = %#v", document)
	}
	if document.UpdatedAt.IsZero() {
		t.Fatal("updatedAt must be populated")
	}
	past := document.UpdatedAt.Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(root, "system.md"), past, past); err != nil {
		t.Fatal(err)
	}
	updatedContent := document.Content + "\nChanged"
	if err := service.Update(UpdateInput{Path: "system.md", Content: &updatedContent}); err != nil {
		t.Fatal(err)
	}
	document, err = service.Get("system.md")
	if err != nil || !document.UpdatedAt.After(past) {
		t.Fatalf("updated document = %#v, %v", document, err)
	}
	graph, err := service.Graph()
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 1 || graph.Nodes[0].PageType != "technical" {
		t.Fatalf("graph nodes = %#v", graph.Nodes)
	}
}

func TestRejectsInvalidPageType(t *testing.T) {
	service, root := newTestService(t)
	err := service.Create(CreateInput{Path: "bad.md", Type: "document", PageType: "unknown"})
	if !errors.Is(err, ErrInvalidPageType) {
		t.Fatalf("error = %v, want invalid page type", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "bad.md")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid document was created: %v", statErr)
	}
}

func TestCreatePageTypeOverridesContentAndKeepsRequiredProperties(t *testing.T) {
	service, _ := newTestService(t)
	content := "---\npage_type: \"incident\"\n---\n\n# System"
	if err := service.Create(CreateInput{Path: "system.md", Type: "document", PageType: "technical", Content: content}); err != nil {
		t.Fatal(err)
	}
	document, err := service.Get("system.md")
	if err != nil {
		t.Fatal(err)
	}
	if document.PageType != "technical" || !strings.Contains(document.Content, `page_type: "technical"`) || !strings.Contains(document.Content, `name: "system"`) || !strings.Contains(document.Content, `description: ""`) {
		t.Fatalf("document = %#v", document)
	}
}

func TestGraphContainsHierarchyAndWikiLinks(t *testing.T) {
	service, _ := newTestService(t)
	for _, input := range []CreateInput{
		{Path: "Product", Type: "directory"},
		{Path: "Product/API", Type: "directory"},
		{Path: "Product/API/reference.md", Type: "document", Content: "# Reference"},
		{Path: "Product/overview.md", Type: "document", Content: "See [[Product/API]] and [[reference]]."},
	} {
		if err := service.Create(input); err != nil {
			t.Fatal(err)
		}
	}
	graph, err := service.Graph()
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 4 {
		t.Fatalf("nodes = %#v", graph.Nodes)
	}
	wanted := map[string]bool{
		"hierarchy\x00Product\x00Product/API":                     false,
		"hierarchy\x00Product/API\x00Product/API/reference.md":    false,
		"hierarchy\x00Product\x00Product/overview.md":             false,
		"link\x00Product/overview.md\x00Product/API":              false,
		"link\x00Product/overview.md\x00Product/API/reference.md": false,
	}
	for _, edge := range graph.Edges {
		key := edge.Type + "\x00" + edge.Source + "\x00" + edge.Target
		if _, ok := wanted[key]; ok {
			wanted[key] = true
		}
	}
	for edge, found := range wanted {
		if !found {
			t.Errorf("missing edge %q in %#v", edge, graph.Edges)
		}
	}
}

func TestGraphDoesNotConnectRootEntries(t *testing.T) {
	service, _ := newTestService(t)
	for _, input := range []CreateInput{
		{Path: "Product", Type: "directory"},
		{Path: "Engineering", Type: "directory"},
		{Path: "home.md", Type: "document"},
	} {
		if err := service.Create(input); err != nil {
			t.Fatal(err)
		}
	}
	graph, err := service.Graph()
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Edges) != 0 {
		t.Fatalf("root entries must not be connected: %#v", graph.Edges)
	}
}

func TestGraphResolvesStableIDLinksAfterRename(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "target.md", Type: "document"}); err != nil {
		t.Fatal(err)
	}
	target, err := service.Get("target.md")
	if err != nil {
		t.Fatal(err)
	}
	renamed := "renamed.md"
	if err := service.Update(UpdateInput{Path: "target.md", NewPath: &renamed}); err != nil {
		t.Fatal(err)
	}
	if err := service.Create(CreateInput{Path: "source.md", Type: "document", Content: "[[id:" + target.ID + "]]"}); err != nil {
		t.Fatal(err)
	}
	graph, err := service.Graph()
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range graph.Edges {
		if edge.Type == "link" && edge.Source == "source.md" && edge.Target == renamed {
			return
		}
	}
	t.Fatalf("stable ID link not resolved after rename: %#v", graph.Edges)
}

func TestRejectsUnsafePaths(t *testing.T) {
	service, _ := newTestService(t)
	paths := []string{"../outside.md", "/tmp/outside.md", "folder/../outside.md", ".hidden.md", "folder\\file.md", "note.txt"}
	for _, path := range paths {
		err := service.Create(CreateInput{Path: path, Type: "document"})
		if !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Create(%q) error = %v, want invalid path", path, err)
		}
	}
	if err := service.Create(CreateInput{Path: "missing/note.md", Type: "document"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("create with missing parent error = %v, want not found", err)
	}
}

func TestDoesNotFollowSymlinks(t *testing.T) {
	service, root := newTestService(t)
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := service.Create(CreateInput{Path: "escape/note.md", Type: "document"})
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("create through symlink error = %v, want invalid path", err)
	}
	if _, err := os.Stat(filepath.Join(target, "note.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file was created outside root, err = %v", err)
	}
}

func TestDoesNotOperateOnNonMarkdownFiles(t *testing.T) {
	service, root := newTestService(t)
	if err := os.WriteFile(filepath.Join(root, "private.txt"), []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete("private.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete non-Markdown file error = %v, want not found", err)
	}
	if _, err := os.Stat(filepath.Join(root, "private.txt")); err != nil {
		t.Fatalf("non-Markdown file was changed: %v", err)
	}
}

func TestRejectsMovingDirectoryInsideItself(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "guides", Type: "directory"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Create(CreateInput{Path: "guides/api", Type: "directory"}); err != nil {
		t.Fatal(err)
	}
	destination := "guides/api/guides"
	err := service.Update(UpdateInput{Path: "guides", NewPath: &destination})
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("move inside itself error = %v, want invalid path", err)
	}
	if _, err := os.Stat(filepath.Join(service.repository.root, "guides")); err != nil {
		t.Fatalf("source directory was altered: %v", err)
	}
}

func TestConcurrentWritesRemainWhole(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "note.md", Type: "document"}); err != nil {
		t.Fatal(err)
	}
	values := []string{"first", "second", "third", "fourth"}
	var group sync.WaitGroup
	for _, value := range values {
		group.Add(1)
		go func(value string) {
			defer group.Done()
			if err := service.Update(UpdateInput{Path: "note.md", Content: &value}); err != nil {
				t.Errorf("update: %v", err)
			}
		}(value)
	}
	group.Wait()
	document, err := service.Get("note.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if strings.HasSuffix(document.Content, "\n\n"+value) {
			return
		}
	}
	t.Errorf("content %q is not one complete concurrent write", document.Content)
}

func TestConfigurableStoragePersistsAndReloads(t *testing.T) {
	base := t.TempDir()
	defaultRoot := filepath.Join(base, "default")
	selectedRoot := filepath.Join(base, "selected")
	configPath := filepath.Join(base, "config", "config.toml")
	service, err := NewConfigurableService(defaultRoot, configPath)
	if err != nil {
		t.Fatal(err)
	}
	if settings := service.Storage(); settings.Configured || settings.Path != defaultRoot {
		t.Fatalf("initial storage = %#v", settings)
	}
	settings, err := service.ConfigureStorage(selectedRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Configured || settings.Path != selectedRoot {
		t.Fatalf("configured storage = %#v", settings)
	}
	if settings.VaultPath != filepath.Join(selectedRoot, VaultDirectoryName) {
		t.Fatalf("vault path = %q", settings.VaultPath)
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if config := string(configData); !strings.Contains(config, "[storage]") || !strings.Contains(config, `path = "`+selectedRoot+`"`) {
		t.Fatalf("persisted config is not TOML: %q", config)
	}
	if err := service.Create(CreateInput{Path: "persisted.md", Type: "document", Content: "saved"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(selectedRoot, VaultDirectoryName, "persisted.md")); err != nil {
		t.Fatalf("document was not stored in the vault: %v", err)
	}
	reloaded, err := NewConfigurableService(defaultRoot, configPath)
	if err != nil {
		t.Fatal(err)
	}
	document, err := reloaded.Get("persisted.md")
	if err != nil || !strings.HasSuffix(document.Content, "\n\nsaved") {
		t.Fatalf("reloaded document = %#v, %v", document, err)
	}
}

func TestApplicationSettingsPersistAndAllowCustomPageType(t *testing.T) {
	base := t.TempDir()
	configPath := filepath.Join(base, "config.toml")
	service, err := NewConfigurableService(filepath.Join(base, "documents"), configPath)
	if err != nil {
		t.Fatal(err)
	}
	types := append(defaultPageTypes(), PageTypeDefinition{ID: "ops-runbook", Label: "Runbook", Description: "Operations", Color: "#123abc"})
	settings, err := service.ConfigureApplication(ApplicationSettingsInput{PageTypes: types})
	if err != nil {
		t.Fatal(err)
	}
	if !containsPageType(settings.PageTypes, "ops-runbook") {
		t.Fatalf("settings = %#v", settings)
	}
	if err := service.Create(CreateInput{Path: "ops.md", Type: "document", PageType: "ops-runbook"}); err != nil {
		t.Fatalf("create custom page type: %v", err)
	}
	document, err := service.Get("ops.md")
	if err != nil || document.PageType != "ops-runbook" {
		t.Fatalf("custom page type document = %#v, %v", document, err)
	}
	reloaded, err := NewConfigurableService(filepath.Join(base, "other-documents"), configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.ApplicationSettings(); !containsPageType(got.PageTypes, "ops-runbook") {
		t.Fatalf("reloaded settings = %#v", got)
	}
}

func TestApplicationSettingsRejectInvalidCustomType(t *testing.T) {
	service, _ := newTestService(t)
	types := append(defaultPageTypes(), PageTypeDefinition{ID: "../bad", Label: "Bad", Color: "#123456"})
	if _, err := service.ConfigureApplication(ApplicationSettingsInput{PageTypes: types}); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("error = %v, want invalid settings", err)
	}
}

func TestConfigurableStorageAcceptsEmptyTOMLSelection(t *testing.T) {
	base := t.TempDir()
	defaultRoot := filepath.Join(base, "default")
	configPath := filepath.Join(base, "config.toml")
	if err := os.WriteFile(configPath, []byte("[storage]\npath = \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := NewConfigurableService(defaultRoot, configPath)
	if err != nil {
		t.Fatal(err)
	}
	if settings := service.Storage(); settings.Configured || settings.Path != defaultRoot {
		t.Fatalf("initial storage = %#v", settings)
	}
}

func TestConfigurableStorageRejectsInvalidTOMLPath(t *testing.T) {
	base := t.TempDir()
	configPath := filepath.Join(base, "config.toml")
	if err := os.WriteFile(configPath, []byte("[storage]\npath = not-quoted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewConfigurableService(filepath.Join(base, "default"), configPath); err == nil {
		t.Fatal("NewConfigurableService accepted an invalid TOML storage path")
	}
}

func TestConfigureStorageRejectsUnsafeLocations(t *testing.T) {
	service, _ := newTestService(t)
	for _, path := range []string{"relative/path", string(os.PathSeparator)} {
		if _, err := service.ConfigureStorage(path); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("ConfigureStorage(%q) error = %v, want invalid path", path, err)
		}
	}
}

func TestConfigureStorageDoesNotNestAnExistingVault(t *testing.T) {
	base := t.TempDir()
	vault := filepath.Join(base, VaultDirectoryName)
	if err := os.Mkdir(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	service, _ := newTestService(t)
	settings, err := service.ConfigureStorage(vault)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Path != base || settings.VaultPath != vault {
		t.Fatalf("storage = %#v", settings)
	}
	if _, err := os.Stat(filepath.Join(vault, VaultDirectoryName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nested vault was created: %v", err)
	}
}

func TestBrowseDirectoriesReturnsOnlySortedRealDirectories(t *testing.T) {
	service, _ := newTestService(t)
	root := t.TempDir()
	for _, name := range []string{"Zulu", "alpha"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(filepath.Join(root, "alpha"), filepath.Join(root, "linked"))
	listing, err := service.BrowseDirectories(root)
	if err != nil {
		t.Fatal(err)
	}
	if listing.Path != root || listing.Parent != filepath.Dir(root) {
		t.Fatalf("listing location = %#v", listing)
	}
	if len(listing.Directories) != 2 || listing.Directories[0].Name != "alpha" || listing.Directories[1].Name != "Zulu" {
		t.Fatalf("directories = %#v", listing.Directories)
	}
}
