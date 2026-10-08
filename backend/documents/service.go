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
	"time"
)

// Service serializes filesystem changes so requests made through this process
// cannot observe half-completed operations.
type Service struct {
	repository        *repository
	mu                sync.RWMutex
	configPath        string
	storagePath       string
	configured        bool
	profile           ProfileSettings
	aiPermissions     Permissions
	profiles          []SavedProfile
	activeProfileID   string
	proposals         map[string]DocumentProposal
	creationUndo      map[string]creationUndo
	creationUndoOrder []string
}

// VaultDirectoryName is the application-owned directory created inside a selected location.
const VaultDirectoryName = "Mnemosys-Vault"

// NewService creates a service with an already confirmed document directory.
func NewService(root string) (*Service, error) {
	s, err := newService(root, root, "", true, defaultProfile(), defaultAIPermissions())
	if err == nil {
		s.profiles = []SavedProfile{{ID: "legacy", ProfileSettings: s.profile, Permissions: s.aiPermissions}}
		s.activeProfileID = "legacy"
	}
	return s, err
}

// NewConfigurableService creates a service backed by a persisted storage selection.
// The default root remains usable until the user confirms a directory.
func NewConfigurableService(defaultRoot, configPath string) (*Service, error) {
	root := defaultRoot
	storagePath := defaultRoot
	configured := false
	var profile ProfileSettings
	aiPermissions := defaultAIPermissions()
	var profiles []SavedProfile
	activeProfileID := ""
	data, err := os.ReadFile(configPath)
	if err == nil {
		config, err := parseApplicationConfig(data)
		if err != nil {
			return nil, fmt.Errorf("decode application config: %w", err)
		}
		profile = config.Profile
		aiPermissions = config.AIPermissions
		profiles = config.Profiles
		activeProfileID = config.ActiveProfileID
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
	s, err := newService(root, storagePath, configPath, configured, profile, aiPermissions)
	if err == nil {
		s.profiles = profiles
		s.activeProfileID = activeProfileID
	}
	return s, err
}

// newService validates the root and constructs the shared service state.
func newService(root, storagePath, configPath string, configured bool, profile ProfileSettings, aiPermissions Permissions) (*Service, error) {
	root, err := prepareStorageRoot(root)
	if err != nil {
		return nil, err
	}
	repository, err := newRepository(root, profileName(profile), profile.Team)
	if err != nil {
		return nil, err
	}
	return &Service{repository: repository, configPath: configPath, storagePath: storagePath, configured: configured, profile: profile, aiPermissions: aiPermissions, proposals: make(map[string]DocumentProposal)}, nil
}

func profileName(profile ProfileSettings) string {
	if profile.Type == ProfileAI && profile.Name != "" {
		return profile.Name
	}
	name := strings.TrimSpace(profile.FirstName + " " + profile.LastName)
	if name != "" {
		return name
	}
	if profile.Type == ProfileAI {
		return "AI"
	}
	return "Human"
}

func (s *Service) allowed(permission bool) bool {
	return s.profile.Type == ProfileHuman || (s.profile.Type == ProfileAI && permission)
}

// Tree returns the complete visible directory and Markdown document hierarchy.
func (s *Service) Tree() ([]Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.allowed(s.aiPermissions.View) {
		return nil, ErrForbidden
	}
	return s.repository.tree()
}

// Graph returns directory hierarchy and explicit document relationships.
func (s *Service) Graph() (Graph, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.allowed(s.aiPermissions.View) {
		return Graph{}, ErrForbidden
	}
	return s.repository.graph()
}

// Get returns one Markdown document by its path relative to the configured root.
func (s *Service) Get(path string) (Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.allowed(s.aiPermissions.View) {
		return Document{}, ErrForbidden
	}
	return s.repository.get(path)
}

// Search performs classic lexical search or augments it with local semantic ranking.
func (s *Service) Search(query string, mode SearchMode, scope string) (SearchResponse, error) {
	query = strings.TrimSpace(query)
	if len(query) > 200 {
		return SearchResponse{}, fmt.Errorf("%w: query must not exceed 200 characters", ErrInvalidSearch)
	}
	if mode == "" {
		mode = SearchLexical
	}
	if mode != SearchNames && mode != SearchLexical && mode != SearchHybrid {
		return SearchResponse{}, fmt.Errorf("%w: mode must be names, lexical or hybrid", ErrInvalidSearch)
	}
	if scope != "" {
		if err := validatePath(scope, false); err != nil {
			return SearchResponse{}, fmt.Errorf("%w: invalid scope", ErrInvalidSearch)
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.allowed(s.aiPermissions.View) {
		return SearchResponse{}, ErrForbidden
	}
	results, err := s.repository.search(query, mode, scope)
	if err != nil {
		return SearchResponse{}, err
	}
	return SearchResponse{Query: query, Mode: mode, Results: results}, nil
}

// Create creates one document or directory within the configured root.
func (s *Service) Create(input CreateInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createLocked(input)
}

func (s *Service) createLocked(input CreateInput) error {
	if !s.allowed(s.aiPermissions.Create) {
		return ErrForbidden
	}
	if input.Type == "document" {
		if input.PageType == "" {
			input.PageType = PageTypeGeneral
		}
		if !containsPageType(input.PageType) {
			return ErrInvalidPageType
		}
		input.Owner = profileName(s.profile)
		input.ModifiedBy = s.profile.Type
	}
	return s.repository.create(input)
}

// Update changes a document's contents, path, or both as one serialized operation.
func (s *Service) Update(input UpdateInput) error {
	_, err := s.UpdateWithResult(input)
	return err
}

// UpdateWithResult writes human changes immediately and turns AI changes into proposals.
func (s *Service) UpdateWithResult(input UpdateInput) (UpdateResult, error) {
	if input.NewPath == nil && input.Content == nil {
		return UpdateResult{}, ErrInvalidPath
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(s.aiPermissions.Edit) {
		return UpdateResult{}, ErrForbidden
	}
	if input.Content != nil && input.BaseRevision == "" {
		return UpdateResult{}, ErrRevisionRequired
	}
	if input.Content != nil && strings.HasSuffix(input.Path, ".md") {
		current, err := s.repository.get(input.Path)
		if err != nil {
			return UpdateResult{}, err
		}
		if current.Revision != input.BaseRevision {
			if s.allowed(s.aiPermissions.View) {
				return UpdateResult{}, &EditConflictError{Current: current}
			}
			return UpdateResult{}, ErrEditConflict
		}
	}
	if s.profile.Type == ProfileAI {
		if input.NewPath != nil {
			return UpdateResult{}, ErrForbidden
		}
		path := input.Path
		if strings.HasSuffix(path, ".md") {
			document, err := s.repository.get(path)
			if err != nil {
				return UpdateResult{}, err
			}
			if !document.AIEditable {
				return UpdateResult{}, ErrForbidden
			}
			proposal, err := s.createProposalLocked(path, document.Content, *input.Content)
			if err != nil {
				return UpdateResult{}, err
			}
			return UpdateResult{Path: path, Proposal: &proposal, Document: &document}, nil
		}
	}
	if input.NewPath != nil {
		if err := s.repository.move(input.Path, *input.NewPath); err != nil {
			return UpdateResult{}, err
		}
		input.Path = *input.NewPath
	}
	if input.Content != nil {
		if err := s.repository.write(input.Path, *input.Content, s.profile.Type); err != nil {
			return UpdateResult{}, err
		}
		document, err := s.repository.get(input.Path)
		if err != nil {
			return UpdateResult{}, err
		}
		return UpdateResult{Path: input.Path, Document: &document}, nil
	}
	if input.NewPath != nil && strings.HasSuffix(input.Path, ".md") {
		document, err := s.repository.get(input.Path)
		if err != nil {
			return UpdateResult{}, err
		}
		if err := s.repository.write(input.Path, document.Content, s.profile.Type); err != nil {
			return UpdateResult{}, err
		}
	}
	return UpdateResult{Path: input.Path}, nil
}

func (s *Service) createProposalLocked(path, original, proposed string) (DocumentProposal, error) {
	proposalID, err := newDocumentID()
	if err != nil {
		return DocumentProposal{}, err
	}
	// Each submission is independent: another agent's draft or reviewed history
	// must never be removed by a subsequent proposal for the same page.
	proposal := DocumentProposal{ID: proposalID, Path: path, OriginalContent: original, ProposedContent: proposed, Diff: markdownDiff(original, proposed), Status: ProposalInReview, CreatedAt: time.Now().UTC()}
	s.proposals[proposal.ID] = proposal
	return proposal, nil
}

// Proposals returns AI work items, including resolved history.
func (s *Service) Proposals() []DocumentProposal {
	s.mu.RLock()
	defer s.mu.RUnlock()
	proposals := make([]DocumentProposal, 0, len(s.proposals))
	for _, proposal := range s.proposals {
		proposals = append(proposals, proposal)
	}
	sort.Slice(proposals, func(i, j int) bool { return proposals[i].CreatedAt.Before(proposals[j].CreatedAt) })
	return proposals
}

// AcceptProposal applies the original or human-edited proposed Markdown.
func (s *Service) AcceptProposal(id string, editedContent *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.profile.Type != ProfileHuman {
		return ErrForbidden
	}
	proposal, ok := s.proposals[id]
	if !ok {
		return ErrNotFound
	}
	if proposal.Status == ProposalRejected || proposal.Status == ProposalMerged {
		return fmt.Errorf("%w: proposal is already %s", ErrInvalidSettings, proposal.Status)
	}
	document, err := s.repository.get(proposal.Path)
	if err != nil {
		return err
	}
	if document.Content != proposal.OriginalContent {
		return &EditConflictError{Current: document}
	}
	content := proposal.ProposedContent
	if editedContent != nil {
		content = *editedContent
	}
	if err := s.repository.write(proposal.Path, content, ProfileHuman); err != nil {
		return err
	}
	proposal.Status = ProposalMerged
	s.proposals[id] = proposal
	return nil
}

func (s *Service) RejectProposal(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	proposal, ok := s.proposals[id]
	if !ok {
		return ErrNotFound
	}
	if proposal.Status == ProposalMerged {
		return fmt.Errorf("%w: merged proposals cannot be rejected", ErrInvalidSettings)
	}
	proposal.Status = ProposalRejected
	s.proposals[id] = proposal
	return nil
}

// SetProposalStatus records a collaboration state without changing the document.
func (s *Service) SetProposalStatus(id string, status ProposalStatus) error {
	if !validProposalStatus(status) {
		return fmt.Errorf("%w: unknown proposal status %q", ErrInvalidSettings, status)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	proposal, ok := s.proposals[id]
	if !ok {
		return ErrNotFound
	}
	if proposal.Status == ProposalMerged || proposal.Status == ProposalRejected {
		return fmt.Errorf("%w: %s proposals are final", ErrInvalidSettings, proposal.Status)
	}
	if status == ProposalMerged || status == ProposalRejected {
		return fmt.Errorf("%w: use the accept or reject action for %s", ErrInvalidSettings, status)
	}
	proposal.Status = status
	s.proposals[id] = proposal
	return nil
}

func validProposalStatus(status ProposalStatus) bool {
	switch status {
	case ProposalDraft, ProposalInReview, ProposalNeedsHumanInput, ProposalApproved, ProposalRejected, ProposalMerged:
		return true
	default:
		return false
	}
}

func markdownDiff(original, proposed string) string {
	oldLines, newLines := strings.Split(original, "\n"), strings.Split(proposed, "\n")
	dp := make([][]int, len(oldLines)+1)
	for i := range dp {
		dp[i] = make([]int, len(newLines)+1)
	}
	for i := len(oldLines) - 1; i >= 0; i-- {
		for j := len(newLines) - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var diff strings.Builder
	diff.WriteString("--- current\n+++ proposed\n")
	for i, j := 0, 0; i < len(oldLines) || j < len(newLines); {
		if i < len(oldLines) && j < len(newLines) && oldLines[i] == newLines[j] {
			diff.WriteString("  " + oldLines[i] + "\n")
			i++
			j++
		} else if j < len(newLines) && (i == len(oldLines) || dp[i][j+1] >= dp[i+1][j]) {
			diff.WriteString("+ " + newLines[j] + "\n")
			j++
		} else {
			diff.WriteString("- " + oldLines[i] + "\n")
			i++
		}
	}
	return diff.String()
}

// Delete recursively removes a document or directory within the configured root.
func (s *Service) Delete(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(s.aiPermissions.Delete) {
		return ErrForbidden
	}
	return s.repository.remove(path)
}

// StoreAsset validates and persists an uploaded image in the internal asset directory.
func (s *Service) StoreAsset(data []byte) (Asset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(s.aiPermissions.Edit) {
		return Asset{}, ErrForbidden
	}
	return s.repository.storeAsset(data)
}

// Asset returns a previously uploaded image by its generated name.
func (s *Service) Asset(name string) (Asset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.allowed(s.aiPermissions.View) {
		return Asset{}, ErrForbidden
	}
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
	return s.applicationSettingsLocked()
}

func (s *Service) applicationSettingsLocked() ApplicationSettings {
	profiles := append([]SavedProfile{}, s.profiles...)
	return ApplicationSettings{PageTypes: defaultPageTypes(), Profile: s.profile, AIPermissions: s.aiPermissions, Profiles: profiles, ActiveProfileID: s.activeProfileID}
}

func (s *Service) configLocked() applicationConfig {
	path := ""
	if s.configured {
		path = s.storagePath
	}
	return applicationConfig{StoragePath: path, Profile: s.profile, AIPermissions: s.aiPermissions, Profiles: s.profiles, ActiveProfileID: s.activeProfileID}
}

func (s *Service) persistProfilesLocked(profiles []SavedProfile, activeID string) error {
	if s.configPath == "" {
		return nil
	}
	config := s.configLocked()
	config.Profiles = profiles
	config.ActiveProfileID = activeID
	return persistApplicationConfig(s.configPath, config)
}

func validateNewProfile(profile ProfileSettings) (ProfileSettings, error) {
	profile, err := validateProfile(profile)
	if err != nil {
		return ProfileSettings{}, err
	}
	if profile.Type == ProfileHuman && (profile.FirstName == "" || profile.LastName == "" || profile.Name != "") {
		return ProfileSettings{}, fmt.Errorf("%w: human profiles require first and last names", ErrInvalidSettings)
	}
	if profile.Type == ProfileAI && (profile.Name == "" || profile.FirstName != "" || profile.LastName != "") {
		return ProfileSettings{}, fmt.Errorf("%w: AI profiles require one name", ErrInvalidSettings)
	}
	return profile, nil
}

func validateProfilePermissions(profile ProfileSettings, permissions Permissions) error {
	if profile.Type == ProfileAI && !permissions.View && (permissions.Create || permissions.Edit || permissions.Delete) {
		return fmt.Errorf("%w: AI view permission is required for other rights", ErrInvalidSettings)
	}
	return nil
}

func (s *Service) CreateProfile(input ProfileInput) (ApplicationSettings, error) {
	profile, err := validateNewProfile(input.Profile)
	if err != nil {
		return ApplicationSettings{}, err
	}
	if err := validateProfilePermissions(profile, input.Permissions); err != nil {
		return ApplicationSettings{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.profile.Type == ProfileAI {
		return ApplicationSettings{}, ErrForbidden
	}
	id, err := newDocumentID()
	if err != nil {
		return ApplicationSettings{}, err
	}
	permissions := input.Permissions
	if profile.Type == ProfileHuman {
		permissions = defaultAIPermissions()
	}
	profiles := append(append([]SavedProfile{}, s.profiles...), SavedProfile{ID: id, ProfileSettings: profile, Permissions: permissions})
	activeID := s.activeProfileID
	if activeID == "" && profile.Type == ProfileHuman {
		activeID = id
	}
	if err := s.persistProfilesLocked(profiles, activeID); err != nil {
		return ApplicationSettings{}, err
	}
	s.profiles, s.activeProfileID = profiles, activeID
	if activeID == id {
		s.activateProfileLocked(profiles[len(profiles)-1])
	}
	return s.applicationSettingsLocked(), nil
}

func (s *Service) activateProfileLocked(entry SavedProfile) {
	s.profile, s.aiPermissions = entry.ProfileSettings, entry.Permissions
	s.repository.defaultOwner = profileName(entry.ProfileSettings)
	s.repository.defaultTeam = entry.Team
}

func (s *Service) SelectProfile(id string) (ApplicationSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.profiles {
		if entry.ID != id {
			continue
		}
		if s.profile.Type == ProfileAI || entry.Type != ProfileHuman {
			return ApplicationSettings{}, ErrForbidden
		}
		if err := s.persistProfilesLocked(s.profiles, id); err != nil {
			return ApplicationSettings{}, err
		}
		s.activeProfileID = id
		s.activateProfileLocked(entry)
		return s.applicationSettingsLocked(), nil
	}
	return ApplicationSettings{}, ErrNotFound
}

func (s *Service) UpdateProfile(id string, input ProfileInput) (ApplicationSettings, error) {
	profile, err := validateNewProfile(input.Profile)
	if err != nil {
		return ApplicationSettings{}, err
	}
	if err := validateProfilePermissions(profile, input.Permissions); err != nil {
		return ApplicationSettings{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.profile.Type != ProfileHuman {
		return ApplicationSettings{}, ErrForbidden
	}
	profiles := append([]SavedProfile{}, s.profiles...)
	for i := range profiles {
		if profiles[i].ID != id {
			continue
		}
		if profiles[i].Type != profile.Type {
			return ApplicationSettings{}, fmt.Errorf("%w: profile type cannot change", ErrInvalidSettings)
		}
		permissions := input.Permissions
		if profile.Type == ProfileHuman {
			permissions = defaultAIPermissions()
		}
		profiles[i] = SavedProfile{ID: id, ProfileSettings: profile, Permissions: permissions}
		if err := s.persistProfilesLocked(profiles, s.activeProfileID); err != nil {
			return ApplicationSettings{}, err
		}
		s.profiles = profiles
		if id == s.activeProfileID {
			s.activateProfileLocked(profiles[i])
		}
		return s.applicationSettingsLocked(), nil
	}
	return ApplicationSettings{}, ErrNotFound
}

func (s *Service) ConfigureApplication(input ApplicationSettingsInput) (ApplicationSettings, error) {
	profile, err := validateProfile(input.Profile)
	if err != nil {
		return ApplicationSettings{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.profile.Type == ProfileAI {
		if input.AIPermissions != s.aiPermissions || profile.Type != ProfileAI {
			return ApplicationSettings{}, ErrForbidden
		}
	}
	profiles := append([]SavedProfile{}, s.profiles...)
	activeID := s.activeProfileID
	if activeID == "" {
		activeID = "legacy"
		profiles = append(profiles, SavedProfile{ID: activeID, ProfileSettings: profile, Permissions: input.AIPermissions})
	} else {
		for i := range profiles {
			if profiles[i].ID == activeID {
				profiles[i].ProfileSettings = profile
				profiles[i].Permissions = input.AIPermissions
				break
			}
		}
	}
	if s.configPath != "" {
		if err := s.persistProfilesLocked(profiles, activeID); err != nil {
			return ApplicationSettings{}, err
		}
	}
	s.profile = profile
	s.aiPermissions = input.AIPermissions
	s.profiles, s.activeProfileID = profiles, activeID
	s.repository.defaultOwner = profileName(profile)
	s.repository.defaultTeam = profile.Team
	return s.applicationSettingsLocked(), nil
}

// ConfigureStorage validates, persists, and activates a new document directory.
func (s *Service) ConfigureStorage(path string) (StorageSettings, error) {
	s.mu.RLock()
	owner := profileName(s.profile)
	s.mu.RUnlock()
	storagePath, root, err := prepareVaultRoot(strings.TrimSpace(path))
	if err != nil {
		return StorageSettings{}, err
	}
	team := ""
	s.mu.RLock()
	team = s.profile.Team
	s.mu.RUnlock()
	repository, err := newRepository(root, owner, team)
	if err != nil {
		return StorageSettings{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	settings := StorageSettings{Path: storagePath, VaultPath: root, Configured: true}
	if s.configPath != "" {
		config := s.configLocked()
		config.StoragePath = storagePath
		if err := persistApplicationConfig(s.configPath, config); err != nil {
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
