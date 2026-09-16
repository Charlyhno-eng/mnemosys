package documents

// Node is an entry in the documentation tree.
type Node struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Type     string `json:"type"` // "directory" or "document"
	Children []Node `json:"children,omitempty"`
}

type Document struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type CreateInput struct {
	Path    string `json:"path"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

type UpdateInput struct {
	Path    string  `json:"path"`
	NewPath *string `json:"newPath"`
	Content *string `json:"content"`
}

type Asset struct {
	Name        string
	ContentType string
	Data        []byte
}

// StorageSettings describes the selected parent, active vault, and confirmation state.
type StorageSettings struct {
	Path       string `json:"path"`
	VaultPath  string `json:"vaultPath"`
	Configured bool   `json:"configured"`
}

// StorageInput is the payload used to select a document directory.
type StorageInput struct {
	Path string `json:"path"`
}

// DirectoryEntry describes one selectable child in the storage browser.
type DirectoryEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// DirectoryListing describes one location in the server-side storage browser.
type DirectoryListing struct {
	Path        string           `json:"path"`
	Parent      string           `json:"parent,omitempty"`
	Directories []DirectoryEntry `json:"directories"`
}

// GraphNode represents a document or directory in the knowledge graph.
type GraphNode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// GraphEdge represents either an automatic hierarchy or an explicit wiki link.
type GraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

// Graph contains the complete set of visible nodes and their relationships.
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}
