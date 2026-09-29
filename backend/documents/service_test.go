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
	if err := service.Update(UpdateInput{Path: "engineering/guide.md", NewPath: &newPath, Content: &content, BaseRevision: document.Revision}); err != nil {
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
	if document.ID == "" || document.AIEditable || document.Application != "" || !frontmatterIDPattern.MatchString(document.Content) || !strings.Contains(document.Content, `name: "Product Guide"`) || !strings.Contains(document.Content, `folder_path: "~"`) || !strings.Contains(document.Content, `ai_editable: false`) || strings.Contains(document.Content, "description:") || !strings.Contains(document.Content, `page_type: "general"`) || !strings.HasSuffix(document.Content, "# Guide") {
		t.Fatalf("document = %#v", document)
	}

	content := "---\nname: \"Custom\"\n---\n\nText"
	if err := service.Update(UpdateInput{Path: "Product Guide.md", Content: &content, BaseRevision: document.Revision}); err != nil {
		t.Fatal(err)
	}
	document, err = service.Get("Product Guide.md")
	if err != nil || strings.Contains(document.Content, "description:") || strings.Count(document.Content, "name:") != 1 {
		t.Fatalf("updated properties = %q, %v", document.Content, err)
	}
}

func TestAIEditingRequiresPerDocumentOptIn(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "locked.md", Type: "document", Content: "# Locked"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfigureApplication(ApplicationSettingsInput{Profile: ProfileSettings{Type: ProfileAI}, AIPermissions: Permissions{View: true, Edit: true}}); err != nil {
		t.Fatal(err)
	}
	content := "# AI attempt"
	document, err := service.Get("locked.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Update(UpdateInput{Path: "locked.md", Content: &content, BaseRevision: document.Revision}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("locked document update error = %v, want forbidden", err)
	}
}

func TestProfileTeamAndFolderPathAreStoredInFrontmatter(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.ConfigureApplication(ApplicationSettingsInput{Profile: ProfileSettings{Type: ProfileHuman, FirstName: "Ada", LastName: "Lovelace", Team: "Robotics"}, AIPermissions: defaultAIPermissions()}); err != nil {
		t.Fatal(err)
	}
	if err := service.Create(CreateInput{Path: "robot", Type: "directory"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Create(CreateInput{Path: "robot/drone", Type: "directory"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Create(CreateInput{Path: "robot/drone/flight.md", Type: "document", Content: "# Flight"}); err != nil {
		t.Fatal(err)
	}
	document, err := service.Get("robot/drone/flight.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(document.Content, `owner: "Ada Lovelace"`) || !strings.Contains(document.Content, `team: "Robotics"`) || !strings.Contains(document.Content, `folder_path: "~/robot/drone"`) {
		t.Fatalf("document metadata = %q", document.Content)
	}
	updated := strings.Replace(strings.Replace(document.Content, `application: ""`, `application: "Flight Control"`, 1), `ai_editable: false`, `ai_editable: true`, 1)
	if err := service.Update(UpdateInput{Path: "robot/drone/flight.md", Content: &updated, BaseRevision: document.Revision}); err != nil {
		t.Fatal(err)
	}
	document, err = service.Get("robot/drone/flight.md")
	if err != nil || document.Application != "Flight Control" || !document.AIEditable {
		t.Fatalf("application metadata = %#v, %v", document, err)
	}
}

func TestExistingDocumentsReceiveSearchableMetadataAtStartup(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "legacy.md")
	modified := time.Date(2024, time.March, 4, 12, 30, 0, 0, time.UTC)
	content := "---\nid: \"11111111-1111-4111-8111-111111111111\"\nname: \"Legacy guide\"\ndescription: \"Migration handbook\"\npage_type: \"business\"\n---\n\n# Legacy"
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(root)
	if err != nil {
		t.Fatal(err)
	}
	document, err := service.Get("legacy.md")
	if err != nil {
		t.Fatal(err)
	}
	if document.Owner != "Human" || !document.UpdatedAt.Equal(modified) || !strings.Contains(document.Content, `updated_at: "2024-03-04T12:30:00Z"`) {
		t.Fatalf("migrated document = %#v", document)
	}
	tree, err := service.Tree()
	if err != nil || len(tree) != 1 {
		t.Fatalf("tree = %#v, %v", tree, err)
	}
	if tree[0].Title != "Legacy guide" || tree[0].Description != "" || tree[0].PageType != "business" || tree[0].Owner != "Human" || !strings.Contains(document.Content, `folder_path: "~"`) || strings.Contains(document.Content, "description:") || !tree[0].UpdatedAt.Equal(modified) {
		t.Fatalf("search metadata = %#v", tree[0])
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
	if err := service.Update(UpdateInput{Path: "before.md", Content: &content, BaseRevision: created.Revision}); err != nil {
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
	all := Permissions{View: true, Create: true, Edit: true, Delete: true}
	if _, err := service.ConfigureApplication(ApplicationSettingsInput{Profile: ProfileSettings{Type: ProfileAI, FirstName: "Ada", LastName: "Agent"}, AIPermissions: all}); err != nil {
		t.Fatal(err)
	}
	if err := service.Create(CreateInput{Path: "system.md", Type: "document", PageType: "technical", Content: "# System"}); err != nil {
		t.Fatal(err)
	}
	document, err := service.Get("system.md")
	if err != nil {
		t.Fatal(err)
	}
	if document.PageType != "technical" || document.Owner != "Ada Agent" || !document.AITouched || document.ModifiedBy != ProfileAI || !strings.Contains(document.Content, `page_type: "technical"`) || !strings.Contains(document.Content, `owner: "Ada Agent"`) || !strings.Contains(document.Content, `ai_touched: true`) || !strings.Contains(document.Content, `updated_at: "`) {
		t.Fatalf("document = %#v", document)
	}
	if document.UpdatedAt.IsZero() {
		t.Fatal("updatedAt must be populated")
	}
	past := document.UpdatedAt.Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(root, "system.md"), past, past); err != nil {
		t.Fatal(err)
	}
	humanService, err := NewService(root)
	if err != nil {
		t.Fatal(err)
	}
	updatedContent := document.Content + "\nChanged"
	if err := humanService.Update(UpdateInput{Path: "system.md", Content: &updatedContent, BaseRevision: document.Revision}); err != nil {
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

func TestUpdateRefreshesFrontmatterTimestamp(t *testing.T) {
	service, _ := newTestService(t)
	content := "---\nupdated_at: \"2020-01-01T00:00:00Z\"\n---\n\nOld"
	if err := service.Create(CreateInput{Path: "dated.md", Type: "document", Content: content}); err != nil {
		t.Fatal(err)
	}
	document, err := service.Get("dated.md")
	if err != nil {
		t.Fatal(err)
	}
	updated := document.Content + "\nChanged"
	if err := service.Update(UpdateInput{Path: "dated.md", Content: &updated, BaseRevision: document.Revision}); err != nil {
		t.Fatal(err)
	}
	document, err = service.Get("dated.md")
	if err != nil || !document.UpdatedAt.After(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)) || strings.Contains(document.Content, `updated_at: "2020-01-01T00:00:00Z"`) {
		t.Fatalf("updated timestamp document = %#v, %v", document, err)
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
	if document.PageType != "technical" || !strings.Contains(document.Content, `page_type: "technical"`) || !strings.Contains(document.Content, `name: "system"`) || strings.Contains(document.Content, "description:") {
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

func TestGraphTreatsWikiLinksAsOrdinaryReferences(t *testing.T) {
	service, _ := newTestService(t)
	for _, input := range []CreateInput{
		{Path: "architecture.md", Type: "document"},
		{Path: "legacy.md", Type: "document"},
		{Path: "service.md", Type: "document", Content: "[[architecture]] [[legacy|Old service]]"},
	} {
		if err := service.Create(input); err != nil {
			t.Fatal(err)
		}
	}
	graph, err := service.Graph()
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{
		"link\x00service.md\x00architecture.md": false,
		"link\x00service.md\x00legacy.md":       false,
	}
	for _, edge := range graph.Edges {
		key := edge.Type + "\x00" + edge.Source + "\x00" + edge.Target
		if _, ok := wanted[key]; ok {
			wanted[key] = true
		}
	}
	for edge, found := range wanted {
		if !found {
			t.Errorf("missing typed edge %q in %#v", edge, graph.Edges)
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

func TestSearchKeepsLexicalResultsAndAddsSemanticMatches(t *testing.T) {
	service, _ := newTestService(t)
	for _, input := range []CreateInput{
		{Path: "deployment.md", Type: "document", Content: "Deployment automation uses a release pipeline."},
		{Path: "release.md", Type: "document", Content: "The release pipeline promotes builds to production."},
		{Path: "unrelated.md", Type: "document", Content: "Customer contact directory."},
	} {
		if err := service.Create(input); err != nil {
			t.Fatal(err)
		}
	}
	lexical, err := service.Search("deployment", SearchLexical, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(lexical.Results) != 1 || lexical.Results[0].Path != "deployment.md" || len(lexical.Results[0].MatchTypes) != 1 || lexical.Results[0].MatchTypes[0] != "lexical" {
		t.Fatalf("lexical results = %#v", lexical.Results)
	}
	hybrid, err := service.Search("deployment", SearchHybrid, "")
	if err != nil {
		t.Fatal(err)
	}
	foundSemantic := false
	for _, result := range hybrid.Results {
		if result.Path == "release.md" && len(result.MatchTypes) == 1 && result.MatchTypes[0] == "semantic" {
			foundSemantic = true
		}
		if result.Path == "unrelated.md" {
			t.Fatalf("unrelated semantic result = %#v", result)
		}
	}
	if !foundSemantic {
		t.Fatalf("hybrid results do not contain semantic relation: %#v", hybrid.Results)
	}
}

func TestSearchFindsFileAndFolderNamesWithoutMatchingMarkdownContent(t *testing.T) {
	service, _ := newTestService(t)
	for _, input := range []CreateInput{
		{Path: "project-quasar", Type: "directory"},
		{Path: "project-quasar/summary.md", Type: "document", Content: "A short summary."},
		{Path: "release-checklist.md", Type: "document", Content: "This page mentions xylophone only in its content."},
	} {
		if err := service.Create(input); err != nil {
			t.Fatal(err)
		}
	}

	folderResults, err := service.Search("project-quasar", SearchNames, "")
	if err != nil {
		t.Fatal(err)
	}
	foundFolder := false
	for _, result := range folderResults.Results {
		if result.Path == "project-quasar" && result.Type == "directory" && len(result.MatchTypes) == 1 && result.MatchTypes[0] == "name" {
			foundFolder = true
		}
	}
	if !foundFolder {
		t.Fatalf("folder name results = %#v", folderResults.Results)
	}

	fileResults, err := service.Search("release-checklist", SearchNames, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(fileResults.Results) != 1 || fileResults.Results[0].Path != "release-checklist.md" || fileResults.Results[0].Snippet != "" {
		t.Fatalf("file name results = %#v", fileResults.Results)
	}
	mcpValue, err := service.callMCPTool("search", map[string]string{"query": "release-checklist", "mode": "names"})
	if err != nil {
		t.Fatalf("MCP name search: %v", err)
	}
	mcpResults, ok := mcpValue.(SearchResponse)
	if !ok || mcpResults.Mode != SearchNames || len(mcpResults.Results) != 1 || mcpResults.Results[0].Path != "release-checklist.md" {
		t.Fatalf("MCP name search results = %#v", mcpValue)
	}

	contentOnlyResults, err := service.Search("xylophone", SearchNames, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(contentOnlyResults.Results) != 0 {
		t.Fatalf("name search returned content-only matches = %#v", contentOnlyResults.Results)
	}
}

func TestSearchFindsEveryFrontmatterField(t *testing.T) {
	service, root := newTestService(t)
	if err := os.Mkdir(filepath.Join(root, "robot"), 0o755); err != nil {
		t.Fatal(err)
	}
	const documentID = "2cc1c695-bf21-42a9-85ec-124ad0472e00"
	const target = `---
id: "2cc1c695-bf21-42a9-85ec-124ad0472e00"
name: "3"
page_type: "general"
owner: "Charly Mercier"
team: "Robotic"
application: "app2"
folder_path: "~/robot"
ai_editable: false
last_modified_by: "human"
ai_touched: false
updated_at: "2026-09-29T15:45:27Z"
---

# A plain page`
	const other = `---
id: "11111111-1111-4111-8111-111111111111"
name: "Else"
page_type: "technical"
owner: "Another owner"
team: "Other team"
application: ""
folder_path: "~"
ai_editable: true
last_modified_by: "ai"
ai_touched: true
updated_at: "2025-01-02T01:02:03Z"
---

Robotic false appears only in this page's body.`
	for path, content := range map[string]string{"robot/record.md": target, "other.md": other} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(path)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	queries := []struct {
		field string
		value string
		query string
	}{
		{"id", documentID, "id: \"" + documentID + "\""},
		{"name", "3", `name: "3"`},
		{"page_type", "general", "page_type: general"},
		{"owner", "Charly Mercier", `owner: "Charly Mercier"`},
		{"team", "Robotic", "TEAM: Robotic"},
		{"application", "app2", "application: app2"},
		{"folder_path", "~/robot", "folder_path: ~/robot"},
		{"ai_editable", "false", "ai_editable: false"},
		{"last_modified_by", "human", "last_modified_by: human"},
		{"ai_touched", "false", "ai_touched: false"},
		{"updated_at", "2026-09-29T15:45:27Z", "updated_at: 2026-09-29T15:45:27Z"},
	}
	for _, query := range queries {
		t.Run(query.field, func(t *testing.T) {
			unqualified, err := service.Search(query.value, SearchLexical, "")
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, result := range unqualified.Results {
				if result.Path == "robot/record.md" {
					found = true
				}
			}
			if !found {
				t.Fatalf("search by value %q missed the page: %#v", query.value, unqualified.Results)
			}

			qualified, err := service.Search(query.query, SearchLexical, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(qualified.Results) != 1 || qualified.Results[0].Path != "robot/record.md" || !strings.HasPrefix(qualified.Results[0].Snippet, query.field+": ") {
				t.Fatalf("search by field %q = %#v", query.query, qualified.Results)
			}
		})
	}
	empty, err := service.Search(`application: ""`, SearchLexical, "")
	if err != nil || len(empty.Results) != 1 || empty.Results[0].Path != "other.md" {
		t.Fatalf("empty metadata value results = %#v, %v", empty.Results, err)
	}
	hybrid, err := service.Search("team: Robotic", SearchHybrid, "")
	if err != nil || len(hybrid.Results) != 1 || hybrid.Results[0].Path != "robot/record.md" {
		t.Fatalf("qualified hybrid results = %#v, %v", hybrid.Results, err)
	}
	wrongField, err := service.Search("team: app2", SearchLexical, "")
	if err != nil || len(wrongField.Results) != 0 {
		t.Fatalf("cross-field results = %#v, %v", wrongField.Results, err)
	}
}

func TestSearchValidatesModeQueryAndScope(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.Search("query", "unknown", ""); !errors.Is(err, ErrInvalidSearch) {
		t.Fatalf("invalid mode error = %v", err)
	}
	if _, err := service.Search(strings.Repeat("x", 201), SearchLexical, ""); !errors.Is(err, ErrInvalidSearch) {
		t.Fatalf("long query error = %v", err)
	}
	if _, err := service.Search("query", SearchLexical, "../outside"); !errors.Is(err, ErrInvalidSearch) {
		t.Fatalf("invalid scope error = %v", err)
	}
	if _, err := service.ConfigureApplication(ApplicationSettingsInput{Profile: ProfileSettings{Type: ProfileAI}, AIPermissions: Permissions{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Search("query", SearchLexical, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("search without view permission error = %v", err)
	}
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
	opened, err := service.Get("note.md")
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for _, value := range values {
		group.Add(1)
		go func(value string) {
			defer group.Done()
			if err := service.Update(UpdateInput{Path: "note.md", Content: &value, BaseRevision: opened.Revision}); err != nil && !errors.Is(err, ErrEditConflict) {
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

func TestConcurrentRevisionWritesRejectStaleEditors(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "shared.md", Type: "document", Content: "initial"}); err != nil {
		t.Fatal(err)
	}
	document, err := service.Get("shared.md")
	if err != nil {
		t.Fatal(err)
	}
	values := []string{"editor one", "editor two", "editor three", "editor four"}
	var group sync.WaitGroup
	var resultMu sync.Mutex
	succeeded, conflicted := 0, 0
	for _, value := range values {
		group.Add(1)
		go func(value string) {
			defer group.Done()
			err := service.Update(UpdateInput{Path: "shared.md", Content: &value, BaseRevision: document.Revision})
			resultMu.Lock()
			defer resultMu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrEditConflict):
				conflicted++
			default:
				t.Errorf("update error = %v", err)
			}
		}(value)
	}
	group.Wait()
	if succeeded != 1 || conflicted != len(values)-1 {
		t.Fatalf("successful writes = %d, conflicts = %d", succeeded, conflicted)
	}
	current, err := service.Get("shared.md")
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision == document.Revision {
		t.Fatal("revision did not change after the accepted write")
	}
}

func TestEditConflictReturnsCurrentDocument(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Create(CreateInput{Path: "shared.md", Type: "document", Content: "initial"}); err != nil {
		t.Fatal(err)
	}
	opened, err := service.Get("shared.md")
	if err != nil {
		t.Fatal(err)
	}
	first := "first save"
	if err := service.Update(UpdateInput{Path: "shared.md", Content: &first, BaseRevision: opened.Revision}); err != nil {
		t.Fatal(err)
	}
	stale := "stale save"
	err = service.Update(UpdateInput{Path: "shared.md", Content: &stale, BaseRevision: opened.Revision})
	var conflict *EditConflictError
	if !errors.As(err, &conflict) || !strings.HasSuffix(conflict.Current.Content, "\n\nfirst save") {
		t.Fatalf("conflict = %#v, error = %v", conflict, err)
	}
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
	if _, err := service.CreateProfile(ProfileInput{Profile: ProfileSettings{Type: ProfileHuman, FirstName: "Grace", LastName: "Hopper"}}); err != nil {
		t.Fatal(err)
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

func TestApplicationProfilePermissionsPersistAndControlOperations(t *testing.T) {
	base := t.TempDir()
	configPath := filepath.Join(base, "config.toml")
	service, err := NewConfigurableService(filepath.Join(base, "documents"), configPath)
	if err != nil {
		t.Fatal(err)
	}
	permissions := Permissions{View: true, Create: true, Edit: true}
	settings, err := service.ConfigureApplication(ApplicationSettingsInput{Profile: ProfileSettings{Type: ProfileAI, FirstName: "Ada", LastName: "Agent"}, AIPermissions: permissions})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Profile.Type != ProfileAI || settings.Profile.FirstName != "Ada" || settings.AIPermissions != permissions || len(settings.PageTypes) != 4 {
		t.Fatalf("settings = %#v", settings)
	}
	if err := service.Create(CreateInput{Path: "ops.md", Type: "document"}); err != nil {
		t.Fatalf("create AI-owned page: %v", err)
	}
	document, err := service.Get("ops.md")
	if err != nil || document.Owner != "Ada Agent" || !document.AITouched {
		t.Fatalf("AI-owned document = %#v, %v", document, err)
	}
	if err := service.Delete("ops.md"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("delete error = %v, want forbidden", err)
	}
	changed := permissions
	changed.Delete = true
	if _, err := service.ConfigureApplication(ApplicationSettingsInput{Profile: settings.Profile, AIPermissions: changed}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("AI changed permissions: %v", err)
	}
	if _, err := service.ConfigureApplication(ApplicationSettingsInput{Profile: ProfileSettings{Type: ProfileHuman}, AIPermissions: permissions}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("AI promoted itself to human: %v", err)
	}
	reloaded, err := NewConfigurableService(filepath.Join(base, "other-documents"), configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.ApplicationSettings(); got.Profile.Type != ProfileAI || got.Profile.FirstName != "Ada" || got.AIPermissions != permissions {
		t.Fatalf("reloaded settings = %#v", got)
	}
}

func TestApplicationSettingsRequireAProfile(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.ConfigureApplication(ApplicationSettingsInput{}); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("error = %v, want invalid settings", err)
	}
}

func TestAIChangesBecomeReviewableProposals(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.ConfigureApplication(ApplicationSettingsInput{Profile: ProfileSettings{Type: ProfileHuman, FirstName: "Grace", LastName: "Hopper"}, AIPermissions: defaultAIPermissions()}); err != nil {
		t.Fatal(err)
	}
	if err := service.Create(CreateInput{Path: "human.md", Type: "document", Content: "Human content"}); err != nil {
		t.Fatal(err)
	}
	humanDocument, err := service.Get("human.md")
	if err != nil {
		t.Fatal(err)
	}
	enabledContent := strings.Replace(humanDocument.Content, "ai_editable: false", "ai_editable: true", 1)
	if err := service.Update(UpdateInput{Path: "human.md", Content: &enabledContent, BaseRevision: humanDocument.Revision}); err != nil {
		t.Fatal(err)
	}
	permissions := Permissions{View: true, Edit: true}
	if _, err := service.ConfigureApplication(ApplicationSettingsInput{Profile: ProfileSettings{Type: ProfileAI, FirstName: "Doc", LastName: "Agent"}, AIPermissions: permissions}); err != nil {
		t.Fatal(err)
	}
	if err := service.Create(CreateInput{Path: "blocked.md", Type: "document"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("AI create error = %v, want forbidden", err)
	}
	opened, err := service.Get("human.md")
	if err != nil {
		t.Fatalf("AI view: %v", err)
	}
	content := "Updated by AI"
	result, err := service.UpdateWithResult(UpdateInput{Path: "human.md", Content: &content, BaseRevision: opened.Revision})
	if err != nil || result.Proposal == nil {
		t.Fatal(err)
	}
	document, err := service.Get("human.md")
	if err != nil {
		t.Fatal(err)
	}
	if document.Owner != "Grace Hopper" || document.ModifiedBy != ProfileHuman || document.Content == content || result.Proposal.Status != ProposalInReview || !strings.Contains(result.Proposal.Diff, "+ Updated by AI") {
		t.Fatalf("AI proposal or document = %#v, %#v", result.Proposal, document)
	}
	if err := service.SetProposalStatus(result.Proposal.ID, ProposalNeedsHumanInput); err != nil {
		t.Fatalf("set proposal status: %v", err)
	}
	if err := service.SetProposalStatus(result.Proposal.ID, ProposalApproved); err != nil {
		t.Fatalf("approve proposal: %v", err)
	}
	service.profile = ProfileSettings{Type: ProfileHuman, FirstName: "Grace", LastName: "Hopper"}
	if err := service.AcceptProposal(result.Proposal.ID, &content); err != nil {
		t.Fatal(err)
	}
	document, err = service.Get("human.md")
	if err != nil || !strings.HasSuffix(document.Content, "\n\nUpdated by AI") || document.ModifiedBy != ProfileHuman {
		t.Fatalf("accepted proposal document = %#v, %v", document, err)
	}
	if proposals := service.Proposals(); len(proposals) != 1 || proposals[0].Status != ProposalMerged {
		t.Fatalf("proposal history = %#v", proposals)
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
