package documents

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
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
}

// VaultDirectoryName is the application-owned directory created inside a selected location.
const VaultDirectoryName = "Mnemosys-Vault"

// NewService creates a service with an already confirmed document directory.
func NewService(root string) (*Service, error) {
	return newService(root, root, "", true)
}

// NewConfigurableService creates a service backed by a persisted storage selection.
// The default root remains usable until the user confirms a directory.
func NewConfigurableService(defaultRoot, configPath string) (*Service, error) {
	root := defaultRoot
	storagePath := defaultRoot
	configured := false
	data, err := os.ReadFile(configPath)
	if err == nil {
		path, err := parseStoragePath(data)
		if err != nil {
			return nil, fmt.Errorf("decode application config: %w", err)
		}
		if path != "" {
			storagePath, root, err = prepareVaultRoot(path)
			if err != nil {
				return nil, err
			}
			configured = true
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read application config: %w", err)
	}
	return newService(root, storagePath, configPath, configured)
}

// newService validates the root and constructs the shared service state.
func newService(root, storagePath, configPath string, configured bool) (*Service, error) {
	root, err := prepareStorageRoot(root)
	if err != nil {
		return nil, err
	}
	repository, err := newRepository(root)
	if err != nil {
		return nil, err
	}
	return &Service{repository: repository, configPath: configPath, storagePath: storagePath, configured: configured}, nil
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
		if err := persistStoragePath(s.configPath, storagePath); err != nil {
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

// parseStoragePath reads the storage.path value from the application's TOML file.
func parseStoragePath(data []byte) (string, error) {
	section := ""
	storagePath := ""
	found := false
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		if section != "storage" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "path" {
			continue
		}
		if found {
			return "", fmt.Errorf("line %d: storage.path is defined more than once", lineNumber)
		}
		decoded, err := strconv.Unquote(strings.TrimSpace(value))
		if err != nil {
			return "", fmt.Errorf("line %d: storage.path must be a quoted string", lineNumber)
		}
		storagePath = strings.TrimSpace(decoded)
		found = true
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read config: %w", err)
	}
	return storagePath, nil
}

// persistStoragePath writes the selected directory through a temporary TOML file.
func persistStoragePath(path, storagePath string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".mnemosys-config-")
	if err != nil {
		return fmt.Errorf("create temporary config file: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure temporary config file: %w", err)
	}
	contents := "# Mnemosys application configuration.\n# The selected directory contains the application-owned Mnemosys-Vault folder.\n\n[storage]\npath = " + strconv.Quote(storagePath) + "\n"
	if _, err := temporary.WriteString(contents); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode application config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync application config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close application config: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		if runtime.GOOS != "windows" {
			return fmt.Errorf("replace application config: %w", err)
		}
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			return fmt.Errorf("replace application config: %w", err)
		}
		if retryErr := os.Rename(temporaryName, path); retryErr != nil {
			return fmt.Errorf("replace application config: %w", retryErr)
		}
	}
	return nil
}
