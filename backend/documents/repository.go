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
)

var (
	ErrNotFound      = errors.New("document not found")
	ErrAlreadyExists = errors.New("a document or folder already exists at this path")
	ErrInvalidPath   = errors.New("invalid document path")
	ErrInvalidType   = errors.New("type must be either directory or document")
	ErrInvalidAsset  = errors.New("only PNG, JPEG, GIF and WebP images are accepted")
)

type repository struct {
	root string
}

var assetExtensions = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp",
}

var wikiLinkPattern = regexp.MustCompile(`\[\[([^\]\n]+)\]\]`)

// newRepository creates filesystem access rooted at one validated directory.
func newRepository(root string) (*repository, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat documents root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("documents root is not a directory")
	}
	return &repository{root: root}, nil
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
			graph.Nodes = append(graph.Nodes, graphNode)
			byPath[node.Path] = graphNode
			for _, alias := range []string{node.Path, strings.TrimSuffix(node.Path, ".md"), node.Name, strings.TrimSuffix(node.Name, ".md")} {
				key := strings.ToLower(alias)
				aliases[key] = append(aliases[key], node.Path)
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
	for index := 1; index < len(tree); index++ {
		appendGraphEdge(&graph.Edges, edges, tree[index-1].Path, tree[index].Path, "hierarchy")
	}

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
			nodes = append(nodes, Node{Name: entry.Name(), Path: path, Type: "document"})
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
	return Document{Path: path, Content: string(content)}, nil
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
	input.Content = ensureDocumentProperties(input.Path, input.Content)
	if err := os.WriteFile(target, []byte(input.Content), 0o644); err != nil {
		return fmt.Errorf("create document: %w", err)
	}
	return nil
}

// write replaces the contents of an existing Markdown document.
func (r *repository) write(path, content string) error {
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
	content = ensureDocumentProperties(path, content)
	if err := os.WriteFile(r.absolute(path), []byte(content), 0o644); err != nil {
		return fmt.Errorf("write document: %w", err)
	}
	return nil
}

// ensureDocumentProperties adds the required name and description frontmatter fields.
func ensureDocumentProperties(path, content string) string {
	name := strings.TrimSuffix(filepath.Base(path), ".md")
	header := "---\nname: " + strconv.Quote(name) + "\ndescription: \"\"\n---\n\n"
	if !strings.HasPrefix(content, "---\n") {
		return header + content
	}
	closing := strings.Index(content[4:], "\n---")
	if closing < 0 {
		return header + content
	}
	closing += 4
	frontmatter := content[4:closing]
	missing := ""
	if !regexp.MustCompile(`(?m)^name\s*:`).MatchString(frontmatter) {
		missing += "name: " + strconv.Quote(name) + "\n"
	}
	if !regexp.MustCompile(`(?m)^description\s*:`).MatchString(frontmatter) {
		missing += "description: \"\"\n"
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
