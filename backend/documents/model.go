package documents

import "time"

type PageType string

const PageTypeGeneral PageType = "general"

type ProfileType string

const (
	ProfileHuman ProfileType = "human"
	ProfileAI    ProfileType = "ai"
)

type PageTypeDefinition struct {
	ID          PageType `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Color       string   `json:"color"`
	BuiltIn     bool     `json:"builtIn"`
}

type ApplicationSettings struct {
	PageTypes     []PageTypeDefinition `json:"pageTypes"`
	Profile       ProfileSettings      `json:"profile"`
	AIPermissions Permissions          `json:"aiPermissions"`
}

type ApplicationSettingsInput struct {
	Profile       ProfileSettings `json:"profile"`
	AIPermissions Permissions     `json:"aiPermissions"`
}

type ProfileSettings struct {
	Type      ProfileType `json:"type"`
	FirstName string      `json:"firstName"`
	LastName  string      `json:"lastName"`
}

type Permissions struct {
	View   bool `json:"view"`
	Create bool `json:"create"`
	Edit   bool `json:"edit"`
	Delete bool `json:"delete"`
}

// Node is an entry in the documentation tree.
type Node struct {
	Name        string      `json:"name"`
	Path        string      `json:"path"`
	Type        string      `json:"type"` // "directory" or "document"
	Children    []Node      `json:"children,omitempty"`
	Title       string      `json:"title,omitempty"`
	Description string      `json:"description,omitempty"`
	PageType    PageType    `json:"pageType,omitempty"`
	Owner       string      `json:"owner,omitempty"`
	ModifiedBy  ProfileType `json:"lastModifiedBy,omitempty"`
	AITouched   bool        `json:"aiTouched,omitempty"`
	UpdatedAt   time.Time   `json:"updatedAt,omitempty"`
}

type Document struct {
	ID         string      `json:"id"`
	Path       string      `json:"path"`
	Content    string      `json:"content"`
	PageType   PageType    `json:"pageType"`
	Owner      string      `json:"owner"`
	ModifiedBy ProfileType `json:"lastModifiedBy"`
	AITouched  bool        `json:"aiTouched"`
	UpdatedAt  time.Time   `json:"updatedAt"`
}

type CreateInput struct {
	Path       string      `json:"path"`
	Type       string      `json:"type"`
	PageType   PageType    `json:"pageType,omitempty"`
	Content    string      `json:"content"`
	Owner      string      `json:"-"`
	ModifiedBy ProfileType `json:"-"`
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
	ID         string   `json:"id"`
	DocumentID string   `json:"documentId,omitempty"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	PageType   PageType `json:"pageType,omitempty"`
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
