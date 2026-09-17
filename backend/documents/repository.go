package documents

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrNotFound        = errors.New("document not found")
	ErrAlreadyExists   = errors.New("a document or folder already exists at this path")
	ErrInvalidPath     = errors.New("invalid document path")
	ErrInvalidType     = errors.New("type must be either directory or document")
	ErrInvalidPageType = errors.New("invalid page type")
	ErrForbidden       = errors.New("operation is not allowed for the active profile")
	ErrInvalidSettings = errors.New("invalid application settings")
	ErrInvalidAsset    = errors.New("only PNG, JPEG, GIF and WebP images are accepted")
)

type repository struct {
	root         string
	defaultOwner string
}

var assetExtensions = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp",
}

var wikiLinkPattern = regexp.MustCompile(`\[\[([^\]\n]+)\]\]`)
var frontmatterPageTypePattern = regexp.MustCompile(`(?m)^page_type\s*:\s*["']?([a-z][a-z0-9_-]{0,31})["']?\s*$`)
var frontmatterPageTypePropertyPattern = regexp.MustCompile(`(?m)^page_type\s*:.*$`)
var frontmatterIDPattern = regexp.MustCompile(`(?m)^id\s*:\s*["']?([0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12})["']?\s*$`)
var frontmatterIDPropertyPattern = regexp.MustCompile(`(?m)^id\s*:.*$`)
var frontmatterOwnerPattern = regexp.MustCompile(`(?m)^owner\s*:\s*(.*)$`)
var frontmatterOwnerPropertyPattern = regexp.MustCompile(`(?m)^owner\s*:.*$`)
var frontmatterModifiedByPattern = regexp.MustCompile(`(?m)^last_modified_by\s*:\s*["']?(human|ai)["']?\s*$`)
var frontmatterModifiedByPropertyPattern = regexp.MustCompile(`(?m)^last_modified_by\s*:.*$`)
var frontmatterAITouchedPattern = regexp.MustCompile(`(?m)^ai_touched\s*:\s*(true|false)\s*$`)
var frontmatterAITouchedPropertyPattern = regexp.MustCompile(`(?m)^ai_touched\s*:.*$`)
var frontmatterUpdatedAtPattern = regexp.MustCompile(`(?m)^updated_at\s*:\s*["']?([^"'\r\n]+)["']?\s*$`)
var frontmatterUpdatedAtPropertyPattern = regexp.MustCompile(`(?m)^updated_at\s*:.*$`)
var frontmatterNamePattern = regexp.MustCompile(`(?m)^name\s*:\s*(.*)$`)
var frontmatterDescriptionPattern = regexp.MustCompile(`(?m)^description\s*:\s*(.*)$`)

func frontmatterValue(content string, pattern *regexp.Regexp) string {
	if !strings.HasPrefix(content, "---\n") {
		return ""
	}
	closing := strings.Index(content[4:], "\n---")
	if closing < 0 {
		return ""
	}
	match := pattern.FindStringSubmatch(content[4 : closing+4])
	if len(match) != 2 {
		return ""
	}
	value := strings.TrimSpace(match[1])
	if unquoted, err := strconv.Unquote(value); err == nil {
		return unquoted
	}
	return strings.Trim(value, "'\"")
}

func pageTypeFromContent(content string) PageType {
	if !strings.HasPrefix(content, "---\n") {
		return PageTypeGeneral
	}
	closing := strings.Index(content[4:], "\n---")
	if closing < 0 {
		return PageTypeGeneral
	}
	match := frontmatterPageTypePattern.FindStringSubmatch(content[4 : closing+4])
	if len(match) != 2 {
		return PageTypeGeneral
	}
	return PageType(match[1])
}

func documentIDFromContent(content string) string {
	if !strings.HasPrefix(content, "---\n") {
		return ""
	}
	closing := strings.Index(content[4:], "\n---")
	if closing < 0 {
		return ""
	}
	match := frontmatterIDPattern.FindStringSubmatch(content[4 : closing+4])
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func ownerFromContent(content, fallback string) string {
	owner := strings.TrimSpace(frontmatterValue(content, frontmatterOwnerPattern))
	if owner == "" || owner == "human" || owner == "ai" {
		return fallback
	}
	return owner
}

func modifiedByFromContent(content string) ProfileType {
	value := ProfileType(frontmatterValue(content, frontmatterModifiedByPattern))
	if value != ProfileHuman && value != ProfileAI {
		return ProfileHuman
	}
	return value
}

func aiTouchedFromContent(content string) bool {
	return frontmatterValue(content, frontmatterAITouchedPattern) == "true"
}

func updatedAtFromContent(content string, fallback time.Time) time.Time {
	if value := frontmatterValue(content, frontmatterUpdatedAtPattern); value != "" {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			return parsed
		}
	}
	return fallback
}

func newDocumentID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate document ID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

// newRepository creates filesystem access rooted at one validated directory.
func newRepository(root, defaultOwner string) (*repository, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat documents root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("documents root is not a directory")
	}
	repository := &repository{root: root, defaultOwner: defaultOwner}
	if err := repository.ensureDocumentIDs(); err != nil {
		return nil, err
	}
	return repository, nil
}

// ensureDocumentIDs backfills missing or duplicate stable IDs in existing Markdown files.
func (r *repository) ensureDocumentIDs() error {
	seen := make(map[string]struct{})
	return filepath.WalkDir(r.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("scan documents for stable IDs: %w", walkErr)
		}
		if path == r.root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") || !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read document while assigning stable ID: %w", err)
		}
		documentID := documentIDFromContent(string(content))
		_, duplicate := seen[documentID]
		relative, err := filepath.Rel(r.root, path)
		if err != nil {
			return fmt.Errorf("resolve document path while assigning metadata: %w", err)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect document while assigning metadata: %w", err)
		}
		rawOwner := frontmatterValue(string(content), frontmatterOwnerPattern)
		owner := ownerFromContent(string(content), r.defaultOwner)
		metadataComplete := owner != "" && owner != "human" && owner != "ai" && frontmatterModifiedByPattern.MatchString(string(content)) && frontmatterAITouchedPattern.MatchString(string(content)) && frontmatterUpdatedAtPattern.MatchString(string(content))
		if documentID != "" && !duplicate && metadataComplete {
			seen[documentID] = struct{}{}
			return nil
		}
		if documentID == "" || duplicate {
			documentID, err = newDocumentID()
			if err != nil {
				return err
			}
		}
		seen[documentID] = struct{}{}
		actor := modifiedByFromContent(string(content))
		forceAITouched := aiTouchedFromContent(string(content))
		if rawOwner == "ai" {
			actor, forceAITouched = ProfileAI, true
		}
		updated := ensureDocumentMetadata(filepath.ToSlash(relative), string(content), "", owner, actor, forceAITouched, documentID, info.ModTime().UTC())
		if err := os.WriteFile(path, []byte(updated), info.Mode().Perm()); err != nil {
			return fmt.Errorf("write document metadata: %w", err)
		}
		if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
			return fmt.Errorf("restore document modification time: %w", err)
		}
		return nil
	})
}

// validatePath accepts only clean relative paths and optionally enforces a Markdown extension.
func validatePath(path string, requireMarkdown bool) error {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "\\") || strings.ContainsRune(path, '\x00') {
		return ErrInvalidPath
	}
	if filepath.Clean(path) != path {
		return ErrInvalidPath
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.HasPrefix(segment, ".") {
			return ErrInvalidPath
		}
	}
	if requireMarkdown && (!strings.HasSuffix(path, ".md") || strings.TrimSuffix(filepath.Base(path), ".md") == "") {
		return fmt.Errorf("%w: document filenames must end in .md", ErrInvalidPath)
	}
	return nil
}

// absolute resolves an already validated relative path below the repository root.
func (r *repository) absolute(path string) string {
	return filepath.Join(r.root, filepath.FromSlash(path))
}

// verifyExistingPath rejects symlinks and makes sure every component is the expected kind.
// No application operation follows a symlink within the document directory.
func (r *repository) verifyExistingPath(path string, finalMustBeDirectory bool) error {
	current := r.root
	for _, segment := range strings.Split(path, "/") {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("inspect path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symbolic links are not allowed", ErrInvalidPath)
		}
		if current != r.absolute(path) && !info.IsDir() {
			return ErrNotFound
		}
	}
	if finalMustBeDirectory {
		info, err := os.Lstat(r.absolute(path))
		if err != nil || !info.IsDir() {
			return ErrNotFound
		}
	}
	return nil
}

// verifyParent ensures the target parent exists, is a directory, and contains no symlink traversal.
func (r *repository) verifyParent(path string) error {
	parent := filepath.Dir(path)
	if parent == "." {
		return nil
	}
	return r.verifyExistingPath(filepath.ToSlash(parent), true)
}

// tree reads the complete visible document hierarchy.
func (r *repository) tree() ([]Node, error) { return r.readDirectory(r.root, "") }

// graph builds automatic hierarchy edges and explicit Obsidian-style wiki links.
func (r *repository) graph() (Graph, error) {
	tree, err := r.tree()
	if err != nil {
		return Graph{}, err
	}
	graph := Graph{Nodes: make([]GraphNode, 0), Edges: make([]GraphEdge, 0)}
	byPath := make(map[string]GraphNode)
	aliases := make(map[string][]string)
	documents := make([]string, 0)
	edges := make(map[string]struct{})

	var visit func(nodes []Node, parent string)
	visit = func(nodes []Node, parent string) {
		for _, node := range nodes {
			graphNode := GraphNode{ID: node.Path, Name: strings.TrimSuffix(node.Name, ".md"), Type: node.Type}
			if node.Type == "document" {
				document, err := r.get(node.Path)
				if err == nil {
					graphNode.PageType = document.PageType
					graphNode.DocumentID = document.ID
				}
			}
			graph.Nodes = append(graph.Nodes, graphNode)
			byPath[node.Path] = graphNode
			for _, alias := range []string{node.Path, strings.TrimSuffix(node.Path, ".md"), node.Name, strings.TrimSuffix(node.Name, ".md")} {
				key := strings.ToLower(alias)
				aliases[key] = append(aliases[key], node.Path)
			}
			if graphNode.DocumentID != "" {
				aliases["id:"+graphNode.DocumentID] = append(aliases["id:"+graphNode.DocumentID], node.Path)
				aliases[graphNode.DocumentID] = append(aliases[graphNode.DocumentID], node.Path)
			}
			if parent != "" {
				appendGraphEdge(&graph.Edges, edges, parent, node.Path, "hierarchy")
			}
			if node.Type == "document" {
				documents = append(documents, node.Path)
			} else {
				visit(node.Children, node.Path)
			}
		}
	}
	visit(tree, "")
	for _, path := range documents {
		document, err := r.get(path)
		if err != nil {
			return Graph{}, err
		}
		for _, match := range wikiLinkPattern.FindAllStringSubmatch(document.Content, -1) {
			target := strings.TrimSpace(strings.SplitN(match[1], "|", 2)[0])
			target = strings.TrimSpace(strings.SplitN(target, "#", 2)[0])
			if resolved := resolveGraphTarget(target, byPath, aliases); resolved != "" && resolved != path {
				appendGraphEdge(&graph.Edges, edges, path, resolved, "link")
			}
		}
	}
	return graph, nil
}

// resolveGraphTarget resolves full paths first and unique names second.
func resolveGraphTarget(target string, byPath map[string]GraphNode, aliases map[string][]string) string {
	if target == "" {
		return ""
	}
	for _, candidate := range []string{target, target + ".md"} {
		if _, ok := byPath[candidate]; ok {
			return candidate
		}
	}
	matches := aliases[strings.ToLower(target)]
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

// appendGraphEdge appends one relationship while removing exact duplicates.
func appendGraphEdge(edges *[]GraphEdge, seen map[string]struct{}, source, target, edgeType string) {
	key := edgeType + "\x00" + source + "\x00" + target
	if _, exists := seen[key]; exists {
		return
	}
	seen[key] = struct{}{}
	*edges = append(*edges, GraphEdge{Source: source, Target: target, Type: edgeType})
}

// readDirectory recursively collects directories and Markdown files in deterministic order.
func (r *repository) readDirectory(absolute, relative string) ([]Node, error) {
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return nil, fmt.Errorf("read documentation tree: %w", err)
	}
	nodes := make([]Node, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := entry.Name()
		if relative != "" {
			path = relative + "/" + path
		}
		if entry.IsDir() {
			children, err := r.readDirectory(filepath.Join(absolute, entry.Name()), path)
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, Node{Name: entry.Name(), Path: path, Type: "directory", Children: children})
		} else if strings.HasSuffix(entry.Name(), ".md") {
			content, err := os.ReadFile(filepath.Join(absolute, entry.Name()))
			if err != nil {
				return nil, fmt.Errorf("read document metadata: %w", err)
			}
			info, err := entry.Info()
			if err != nil {
				return nil, fmt.Errorf("inspect document metadata: %w", err)
			}
			text := string(content)
			nodes = append(nodes, Node{Name: entry.Name(), Path: path, Type: "document", Title: frontmatterValue(text, frontmatterNamePattern), Description: frontmatterValue(text, frontmatterDescriptionPattern), PageType: pageTypeFromContent(text), Owner: ownerFromContent(text, r.defaultOwner), ModifiedBy: modifiedByFromContent(text), AITouched: aiTouchedFromContent(text), UpdatedAt: updatedAtFromContent(text, info.ModTime())})
		}
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Type != nodes[j].Type {
			return nodes[i].Type == "directory"
		}
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
	return nodes, nil
}

// get reads one Markdown document without following symbolic links.
func (r *repository) get(path string) (Document, error) {
	if err := validatePath(path, true); err != nil {
		return Document{}, err
	}
	if err := r.verifyExistingPath(path, false); err != nil {
		return Document{}, err
	}
	info, err := os.Lstat(r.absolute(path))
	if err != nil {
		return Document{}, ErrNotFound
	}
	if info.IsDir() {
		return Document{}, ErrNotFound
	}
	content, err := os.ReadFile(r.absolute(path))
	if errors.Is(err, fs.ErrNotExist) {
		return Document{}, ErrNotFound
	}
	if err != nil {
		return Document{}, fmt.Errorf("read document: %w", err)
	}
	return Document{ID: documentIDFromContent(string(content)), Path: path, Content: string(content), PageType: pageTypeFromContent(string(content)), Owner: ownerFromContent(string(content), r.defaultOwner), ModifiedBy: modifiedByFromContent(string(content)), AITouched: aiTouchedFromContent(string(content)), UpdatedAt: updatedAtFromContent(string(content), info.ModTime())}, nil
}

// create adds one document or directory without overwriting existing data.
func (r *repository) create(input CreateInput) error {
	if input.Type != "directory" && input.Type != "document" {
		return ErrInvalidType
	}
	if err := validatePath(input.Path, input.Type == "document"); err != nil {
		return err
	}
	if err := r.verifyParent(input.Path); err != nil {
		return err
	}
	target := r.absolute(input.Path)
	if _, err := os.Lstat(target); err == nil {
		return ErrAlreadyExists
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect target: %w", err)
	}
	if input.Type == "directory" {
		if err := os.Mkdir(target, 0o755); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}
		return nil
	}
	documentID, err := newDocumentID()
	if err != nil {
		return err
	}
	input.Content = ensureDocumentMetadata(input.Path, input.Content, input.PageType, input.Owner, input.ModifiedBy, false, documentID, time.Now().UTC())
	if err := os.WriteFile(target, []byte(input.Content), 0o644); err != nil {
		return fmt.Errorf("create document: %w", err)
	}
	return nil
}

// write replaces the contents of an existing Markdown document.
func (r *repository) write(path, content string, actor ProfileType) error {
	if err := validatePath(path, true); err != nil {
		return err
	}
	if err := r.verifyExistingPath(path, false); err != nil {
		return err
	}
	info, err := os.Lstat(r.absolute(path))
	if err != nil || info.IsDir() {
		return ErrNotFound
	}
	existing, err := os.ReadFile(r.absolute(path))
	if err != nil {
		return fmt.Errorf("read document identity: %w", err)
	}
	documentID := documentIDFromContent(string(existing))
	if documentID == "" {
		documentID, err = newDocumentID()
		if err != nil {
			return err
		}
	}
	content = ensureDocumentMetadata(path, content, "", ownerFromContent(string(existing), r.defaultOwner), actor, aiTouchedFromContent(string(existing)), documentID, time.Now().UTC())
	if err := os.WriteFile(r.absolute(path), []byte(content), 0o644); err != nil {
		return fmt.Errorf("write document: %w", err)
	}
	return nil
}

// ensureDocumentProperties adds all required frontmatter fields for legacy callers.
func ensureDocumentProperties(path, content string, requestedPageType PageType, documentID string) string {
	return ensureDocumentMetadata(path, content, requestedPageType, "Human", ProfileHuman, false, documentID, time.Now().UTC())
}

// ensureDocumentMetadata keeps all AI-relevant metadata inside the Markdown file.
func ensureDocumentMetadata(path, content string, requestedPageType PageType, requestedOwner string, actor ProfileType, forceAITouched bool, documentID string, updatedAt time.Time) string {
	name := strings.TrimSuffix(filepath.Base(path), ".md")
	pageType := requestedPageType
	if pageType == "" {
		pageType = pageTypeFromContent(content)
	}
	owner := requestedOwner
	if owner == "" {
		owner = "Human"
	}
	if actor != ProfileAI {
		actor = ProfileHuman
	}
	aiTouched := forceAITouched || aiTouchedFromContent(content) || actor == ProfileAI
	updated := updatedAt.UTC().Format(time.RFC3339)
	header := "---\nid: " + strconv.Quote(documentID) + "\nname: " + strconv.Quote(name) + "\ndescription: \"\"\npage_type: " + strconv.Quote(string(pageType)) + "\nowner: " + strconv.Quote(owner) + "\nlast_modified_by: " + strconv.Quote(string(actor)) + "\nai_touched: " + strconv.FormatBool(aiTouched) + "\nupdated_at: " + strconv.Quote(updated) + "\n---\n\n"
	if !strings.HasPrefix(content, "---\n") {
		return header + content
	}
	closing := strings.Index(content[4:], "\n---")
	if closing < 0 {
		return header + content
	}
	closing += 4
	frontmatter := content[4:closing]
	if frontmatterIDPropertyPattern.MatchString(frontmatter) {
		frontmatter = frontmatterIDPropertyPattern.ReplaceAllString(frontmatter, "id: "+strconv.Quote(documentID))
		content = content[:4] + frontmatter + content[closing:]
		closing = 4 + len(frontmatter)
	}
	if frontmatterPageTypePropertyPattern.MatchString(frontmatter) {
		frontmatter = frontmatterPageTypePropertyPattern.ReplaceAllString(frontmatter, "page_type: "+strconv.Quote(string(pageType)))
		content = content[:4] + frontmatter + content[closing:]
		closing = 4 + len(frontmatter)
	}
	if frontmatterOwnerPropertyPattern.MatchString(frontmatter) {
		frontmatter = frontmatterOwnerPropertyPattern.ReplaceAllString(frontmatter, "owner: "+strconv.Quote(owner))
		content = content[:4] + frontmatter + content[closing:]
		closing = 4 + len(frontmatter)
	}
	if frontmatterModifiedByPropertyPattern.MatchString(frontmatter) {
		frontmatter = frontmatterModifiedByPropertyPattern.ReplaceAllString(frontmatter, "last_modified_by: "+strconv.Quote(string(actor)))
		content = content[:4] + frontmatter + content[closing:]
		closing = 4 + len(frontmatter)
	}
	if frontmatterAITouchedPropertyPattern.MatchString(frontmatter) {
		frontmatter = frontmatterAITouchedPropertyPattern.ReplaceAllString(frontmatter, "ai_touched: "+strconv.FormatBool(aiTouched))
		content = content[:4] + frontmatter + content[closing:]
		closing = 4 + len(frontmatter)
	}
	if frontmatterUpdatedAtPropertyPattern.MatchString(frontmatter) {
		frontmatter = frontmatterUpdatedAtPropertyPattern.ReplaceAllString(frontmatter, "updated_at: "+strconv.Quote(updated))
		content = content[:4] + frontmatter + content[closing:]
		closing = 4 + len(frontmatter)
	}
	missing := ""
	if !frontmatterIDPropertyPattern.MatchString(frontmatter) {
		missing += "id: " + strconv.Quote(documentID) + "\n"
	}
	if !regexp.MustCompile(`(?m)^name\s*:`).MatchString(frontmatter) {
		missing += "name: " + strconv.Quote(name) + "\n"
	}
	if !regexp.MustCompile(`(?m)^description\s*:`).MatchString(frontmatter) {
		missing += "description: \"\"\n"
	}
	if !frontmatterPageTypePropertyPattern.MatchString(frontmatter) {
		missing += "page_type: " + strconv.Quote(string(pageType)) + "\n"
	}
	if !frontmatterOwnerPropertyPattern.MatchString(frontmatter) {
		missing += "owner: " + strconv.Quote(owner) + "\n"
	}
	if !frontmatterModifiedByPropertyPattern.MatchString(frontmatter) {
		missing += "last_modified_by: " + strconv.Quote(string(actor)) + "\n"
	}
	if !frontmatterAITouchedPropertyPattern.MatchString(frontmatter) {
		missing += "ai_touched: " + strconv.FormatBool(aiTouched) + "\n"
	}
	if !frontmatterUpdatedAtPropertyPattern.MatchString(frontmatter) {
		missing += "updated_at: " + strconv.Quote(updated) + "\n"
	}
	if missing == "" {
		return content
	}
	return content[:closing] + "\n" + missing + content[closing+1:]
}

// move renames or relocates a document or directory while preventing invalid nesting.
func (r *repository) move(source, destination string) error {
	if err := validatePath(source, false); err != nil {
		return err
	}
	if err := validatePath(destination, false); err != nil {
		return err
	}
	info, err := os.Lstat(r.absolute(source))
	if err != nil {
		return ErrNotFound
	}
	if !info.IsDir() && (!strings.HasSuffix(source, ".md") || !strings.HasSuffix(destination, ".md")) {
		return fmt.Errorf("%w: documents must remain Markdown files", ErrInvalidPath)
	}
	if info.IsDir() && (destination == source || strings.HasPrefix(destination, source+"/")) {
		return fmt.Errorf("%w: a directory cannot be moved inside itself", ErrInvalidPath)
	}
	if err := r.verifyExistingPath(source, false); err != nil {
		return err
	}
	if err := r.verifyParent(destination); err != nil {
		return err
	}
	if _, err := os.Lstat(r.absolute(destination)); err == nil {
		return ErrAlreadyExists
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect destination: %w", err)
	}
	if err := os.Rename(r.absolute(source), r.absolute(destination)); err != nil {
		return fmt.Errorf("move document: %w", err)
	}
	return nil
}

// remove deletes one Markdown document or one directory tree.
func (r *repository) remove(path string) error {
	if err := validatePath(path, false); err != nil {
		return err
	}
	if err := r.verifyExistingPath(path, false); err != nil {
		return err
	}
	info, err := os.Lstat(r.absolute(path))
	if err != nil {
		return ErrNotFound
	}
	if !info.IsDir() && !strings.HasSuffix(path, ".md") {
		return ErrNotFound
	}
	if err := os.RemoveAll(r.absolute(path)); err != nil {
		return fmt.Errorf("delete document: %w", err)
	}
	return nil
}

// storeAsset validates image bytes and stores them under an unguessable immutable name.
func (r *repository) storeAsset(data []byte) (Asset, error) {
	contentType := http.DetectContentType(data)
	extension, allowed := assetExtensions[contentType]
	if !allowed {
		return Asset{}, ErrInvalidAsset
	}
	directory := filepath.Join(r.root, ".assets")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return Asset{}, fmt.Errorf("create assets directory: %w", err)
	}
	for range 4 {
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return Asset{}, fmt.Errorf("generate asset name: %w", err)
		}
		name := hex.EncodeToString(random) + extension
		file, err := os.OpenFile(filepath.Join(directory, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return Asset{}, fmt.Errorf("create asset: %w", err)
		}
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			_ = os.Remove(filepath.Join(directory, name))
			return Asset{}, fmt.Errorf("write asset: %w", err)
		}
		if err := file.Close(); err != nil {
			return Asset{}, fmt.Errorf("close asset: %w", err)
		}
		return Asset{Name: name, ContentType: contentType, Data: data}, nil
	}
	return Asset{}, errors.New("could not allocate a unique asset name")
}

// asset reads one generated asset name without accepting arbitrary filesystem paths.
func (r *repository) asset(name string) (Asset, error) {
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	if len(stem) != 32 || strings.Contains(name, "/") {
		return Asset{}, ErrNotFound
	}
	if _, err := hex.DecodeString(stem); err != nil {
		return Asset{}, ErrNotFound
	}
	var contentType string
	for candidateType, candidateExtension := range assetExtensions {
		if extension == candidateExtension {
			contentType = candidateType
			break
		}
	}
	if contentType == "" {
		return Asset{}, ErrNotFound
	}
	data, err := os.ReadFile(filepath.Join(r.root, ".assets", name))
	if errors.Is(err, fs.ErrNotExist) {
		return Asset{}, ErrNotFound
	}
	if err != nil {
		return Asset{}, fmt.Errorf("read asset: %w", err)
	}
	return Asset{Name: name, ContentType: contentType, Data: data}, nil
}
