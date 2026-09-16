package documents

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Service serializes filesystem changes so requests made through this process
// cannot observe half-completed operations.
type Service struct {
	repository  *repository
	mu          sync.RWMutex
	configPath  string
	storagePath string
	configured  bool
	pageTypes   []PageTypeDefinition
}

// VaultDirectoryName is the application-owned directory created inside a selected location.
const VaultDirectoryName = "Mnemosys-Vault"

// NewService creates a service with an already confirmed document directory.
func NewService(root string) (*Service, error) {
	return newService(root, root, "", true, defaultPageTypes())
}

// NewConfigurableService creates a service backed by a persisted storage selection.
// The default root remains usable until the user confirms a directory.
func NewConfigurableService(defaultRoot, configPath string) (*Service, error) {
	root := defaultRoot
	storagePath := defaultRoot
	configured := false
	pageTypes := defaultPageTypes()
	data, err := os.ReadFile(configPath)
	if err == nil {
		config, err := parseApplicationConfig(data)
		if err != nil {
			return nil, fmt.Errorf("decode application config: %w", err)
		}
		pageTypes = config.PageTypes
		if config.StoragePath != "" {
			storagePath, root, err = prepareVaultRoot(config.StoragePath)
			if err != nil {
				return nil, err
			}
			configured = true
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read application config: %w", err)
	}
	return newService(root, storagePath, configPath, configured, pageTypes)
}

// newService validates the root and constructs the shared service state.
func newService(root, storagePath, configPath string, configured bool, pageTypes []PageTypeDefinition) (*Service, error) {
	root, err := prepareStorageRoot(root)
	if err != nil {
		return nil, err
	}
	repository, err := newRepository(root)
	if err != nil {
		return nil, err
	}
	return &Service{repository: repository, configPath: configPath, storagePath: storagePath, configured: configured, pageTypes: clonePageTypes(pageTypes)}, nil
}

// Tree returns the complete visible directory and Markdown document hierarchy.
func (s *Service) Tree() ([]Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.repository.tree()
}

// Graph returns directory hierarchy and explicit document relationships.
func (s *Service) Graph() (Graph, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.repository.graph()
}

// Get returns one Markdown document by its path relative to the configured root.
func (s *Service) Get(path string) (Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.repository.get(path)
}

// Create creates one document or directory within the configured root.
func (s *Service) Create(input CreateInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.Type == "document" {
		if input.PageType == "" {
			input.PageType = PageTypeGeneral
		}
		if !containsPageType(s.pageTypes, input.PageType) {
			return ErrInvalidPageType
		}
	}
	return s.repository.create(input)
}

// Update changes a document's contents, path, or both as one serialized operation.
func (s *Service) Update(input UpdateInput) error {
	if input.NewPath == nil && input.Content == nil {
		return ErrInvalidPath
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.NewPath != nil {
		if err := s.repository.move(input.Path, *input.NewPath); err != nil {
			return err
		}
		input.Path = *input.NewPath
	}
	if input.Content != nil {
		return s.repository.write(input.Path, *input.Content)
	}
	return nil
}

// Delete recursively removes a document or directory within the configured root.
func (s *Service) Delete(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.repository.remove(path)
}

// StoreAsset validates and persists an uploaded image in the internal asset directory.
func (s *Service) StoreAsset(data []byte) (Asset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.repository.storeAsset(data)
}

// Asset returns a previously uploaded image by its generated name.
func (s *Service) Asset(name string) (Asset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.repository.asset(name)
}

// Storage returns the selected parent, active vault, and confirmation state.
func (s *Service) Storage() StorageSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return StorageSettings{Path: s.storagePath, VaultPath: s.repository.root, Configured: s.configured}
}

func (s *Service) ApplicationSettings() ApplicationSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ApplicationSettings{PageTypes: clonePageTypes(s.pageTypes)}
}

func (s *Service) ConfigureApplication(input ApplicationSettingsInput) (ApplicationSettings, error) {
	pageTypes, err := validatePageTypes(input.PageTypes)
	if err != nil {
		return ApplicationSettings{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.configPath != "" {
		storagePath := ""
		if s.configured {
			storagePath = s.storagePath
		}
		if err := persistApplicationConfig(s.configPath, applicationConfig{StoragePath: storagePath, PageTypes: pageTypes}); err != nil {
			return ApplicationSettings{}, err
		}
	}
	s.pageTypes = clonePageTypes(pageTypes)
	return ApplicationSettings{PageTypes: clonePageTypes(s.pageTypes)}, nil
}

// ConfigureStorage validates, persists, and activates a new document directory.
func (s *Service) ConfigureStorage(path string) (StorageSettings, error) {
	storagePath, root, err := prepareVaultRoot(strings.TrimSpace(path))
	if err != nil {
		return StorageSettings{}, err
	}
	repository, err := newRepository(root)
	if err != nil {
		return StorageSettings{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	settings := StorageSettings{Path: storagePath, VaultPath: root, Configured: true}
	if s.configPath != "" {
		if err := persistApplicationConfig(s.configPath, applicationConfig{StoragePath: storagePath, PageTypes: s.pageTypes}); err != nil {
			return StorageSettings{}, err
		}
	}
	s.repository = repository
	s.storagePath = storagePath
	s.configured = true
	return settings, nil
}

// prepareVaultRoot creates and validates Mnemosys-Vault inside a selected location.
func prepareVaultRoot(path string) (string, string, error) {
	if filepath.Base(filepath.Clean(path)) == VaultDirectoryName {
		path = filepath.Dir(filepath.Clean(path))
	}
	storagePath, err := prepareStorageRoot(path)
	if err != nil {
		return "", "", err
	}
	vaultPath, err := prepareStorageRoot(filepath.Join(storagePath, VaultDirectoryName))
	if err != nil {
		return "", "", err
	}
	return storagePath, vaultPath, nil
}

// BrowseDirectories lists real child directories for the local storage picker.
func (s *Service) BrowseDirectories(path string) (DirectoryListing, error) {
	if strings.TrimSpace(path) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return DirectoryListing{}, fmt.Errorf("resolve user home directory: %w", err)
		}
		path = home
	}
	if !filepath.IsAbs(path) {
		return DirectoryListing{}, fmt.Errorf("%w: directory browser path must be absolute", ErrInvalidPath)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return DirectoryListing{}, fmt.Errorf("browse directory: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return DirectoryListing{}, fmt.Errorf("%w: directory is unavailable", ErrInvalidPath)
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return DirectoryListing{}, fmt.Errorf("browse directory: %w", err)
	}
	directories := make([]DirectoryEntry, 0)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		directories = append(directories, DirectoryEntry{Name: entry.Name(), Path: filepath.Join(resolved, entry.Name())})
	}
	sort.Slice(directories, func(i, j int) bool {
		return strings.ToLower(directories[i].Name) < strings.ToLower(directories[j].Name)
	})
	parent := filepath.Dir(resolved)
	if parent == resolved {
		parent = ""
	}
	return DirectoryListing{Path: resolved, Parent: parent, Directories: directories}, nil
}

// prepareStorageRoot resolves a safe absolute directory and verifies write access.
func prepareStorageRoot(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: storage path must be absolute", ErrInvalidPath)
	}
	root := filepath.Clean(path)
	if filepath.Dir(root) == root {
		return "", fmt.Errorf("%w: filesystem root cannot be used as document storage", ErrInvalidPath)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create storage directory: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", fmt.Errorf("inspect storage directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: storage path must be a real directory", ErrInvalidPath)
	}
	testFile, err := os.CreateTemp(root, ".mnemosys-write-test-")
	if err != nil {
		return "", fmt.Errorf("storage directory is not writable: %w", err)
	}
	testName := testFile.Name()
	if err := testFile.Close(); err != nil {
		return "", fmt.Errorf("close storage test file: %w", err)
	}
	if err := os.Remove(testName); err != nil {
		return "", fmt.Errorf("remove storage test file: %w", err)
	}
	return root, nil
}
