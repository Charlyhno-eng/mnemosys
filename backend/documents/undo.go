package documents

import (
	"fmt"
	"os"
)

type creationUndo struct {
	path, root, profile, revision string
	info                          os.FileInfo
}

// CreateWithUndo captures the exact newly created entry under the mutation lock.
func (s *Service) CreateWithUndo(input CreateInput) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, err := newDocumentID()
	if err != nil {
		return "", err
	}
	if err := s.createLocked(input); err != nil {
		return "", err
	}
	info, err := os.Lstat(s.repository.absolute(input.Path))
	if err != nil {
		return "", fmt.Errorf("inspect created entry: %w", err)
	}
	record := creationUndo{path: input.Path, root: s.repository.root, profile: s.activeProfileID, info: info}
	if !info.IsDir() {
		document, err := s.repository.get(input.Path)
		if err != nil {
			return "", err
		}
		record.revision = document.Revision
	}
	if s.creationUndo == nil {
		s.creationUndo = make(map[string]creationUndo)
	}
	// Bound server memory; the browser also keeps only the latest 100 creations.
	s.creationUndoOrder = append(s.creationUndoOrder, token)
	if len(s.creationUndoOrder) > 1000 {
		delete(s.creationUndo, s.creationUndoOrder[0])
		s.creationUndoOrder = s.creationUndoOrder[1:]
	}
	s.creationUndo[token] = record
	return token, nil
}

// UndoCreate removes only the unchanged entry, never a recursive directory tree.
func (s *Service) UndoCreate(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed(s.aiPermissions.Delete) {
		return ErrForbidden
	}
	record, ok := s.creationUndo[token]
	if !ok {
		return ErrNotFound
	}
	if record.root != s.repository.root || record.profile != s.activeProfileID {
		return ErrForbidden
	}
	if err := s.repository.verifyExistingPath(record.path, false); err != nil {
		return err
	}
	info, err := os.Lstat(s.repository.absolute(record.path))
	if err != nil {
		return fmt.Errorf("inspect undo entry: %w", err)
	}
	conflict := fmt.Errorf("%w: the created item changed; creation cannot be undone", ErrEditConflict)
	if !os.SameFile(record.info, info) {
		return conflict
	}
	if info.IsDir() {
		children, err := os.ReadDir(s.repository.absolute(record.path))
		if err != nil {
			return err
		}
		if len(children) != 0 {
			return conflict
		}
	} else {
		document, err := s.repository.get(record.path)
		if err != nil {
			return err
		}
		if document.Revision != record.revision {
			return conflict
		}
	}
	if err := os.Remove(s.repository.absolute(record.path)); err != nil {
		return fmt.Errorf("undo creation: %w", err)
	}
	delete(s.creationUndo, token)
	return nil
}
