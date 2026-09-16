package documents

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

var pageTypeIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
var colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type applicationConfig struct {
	StoragePath string
	PageTypes   []PageTypeDefinition
}

func defaultPageTypes() []PageTypeDefinition {
	return []PageTypeDefinition{
		{ID: "general", Label: "General", Description: "General-purpose documentation.", Color: "#62a6e8", BuiltIn: true},
		{ID: "business", Label: "Business documentation", Description: "Business rules, processes and functional knowledge.", Color: "#4bc49a", BuiltIn: true},
		{ID: "technical", Label: "Technical documentation", Description: "Architecture, implementation, APIs and operations.", Color: "#a78bfa", BuiltIn: true},
		{ID: "incident", Label: "Incident documentation", Description: "Timeline, impact, root cause and corrective actions.", Color: "#f16f78", BuiltIn: true},
	}
}

func clonePageTypes(values []PageTypeDefinition) []PageTypeDefinition {
	return append([]PageTypeDefinition(nil), values...)
}

func containsPageType(values []PageTypeDefinition, pageType PageType) bool {
	for _, value := range values {
		if value.ID == pageType {
			return true
		}
	}
	return false
}

func validatePageTypes(values []PageTypeDefinition) ([]PageTypeDefinition, error) {
	if len(values) < 4 || len(values) > 32 {
		return nil, fmt.Errorf("%w: page types must contain between 4 and 32 entries", ErrInvalidSettings)
	}
	seen := make(map[PageType]struct{}, len(values))
	result := clonePageTypes(values)
	for index := range result {
		value := &result[index]
		if !pageTypeIDPattern.MatchString(string(value.ID)) {
			return nil, fmt.Errorf("%w: invalid page type identifier %q", ErrInvalidSettings, value.ID)
		}
		if _, exists := seen[value.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate page type %q", ErrInvalidSettings, value.ID)
		}
		seen[value.ID] = struct{}{}
		value.Label = strings.TrimSpace(value.Label)
		value.Description = strings.TrimSpace(value.Description)
		if value.Label == "" || len(value.Label) > 80 || len(value.Description) > 300 {
			return nil, fmt.Errorf("%w: invalid labels for page type %q", ErrInvalidSettings, value.ID)
		}
		if !colorPattern.MatchString(value.Color) {
			return nil, fmt.Errorf("%w: invalid color for page type %q", ErrInvalidSettings, value.ID)
		}
		value.Color = strings.ToLower(value.Color)
		value.BuiltIn = value.ID == "general" || value.ID == "business" || value.ID == "technical" || value.ID == "incident"
	}
	for _, required := range []PageType{"general", "business", "technical", "incident"} {
		if _, exists := seen[required]; !exists {
			return nil, fmt.Errorf("%w: required page type %q is missing", ErrInvalidSettings, required)
		}
	}
	return result, nil
}

func parseApplicationConfig(data []byte) (applicationConfig, error) {
	config := applicationConfig{PageTypes: defaultPageTypes()}
	section := ""
	pageTypes := make([]PageTypeDefinition, 0)
	pageTypeIndexes := make(map[PageType]int)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			if strings.HasPrefix(section, "page_types.") {
				id := PageType(strings.TrimPrefix(section, "page_types."))
				if !pageTypeIDPattern.MatchString(string(id)) {
					return applicationConfig{}, fmt.Errorf("line %d: invalid page type section", lineNumber)
				}
				if _, exists := pageTypeIndexes[id]; !exists {
					pageTypeIndexes[id] = len(pageTypes)
					pageTypes = append(pageTypes, PageTypeDefinition{ID: id})
				}
			}
			continue
		}
		key, rawValue, ok := strings.Cut(line, "=")
		if !ok {
			return applicationConfig{}, fmt.Errorf("line %d: expected key = value", lineNumber)
		}
		key = strings.TrimSpace(key)
		value, err := strconv.Unquote(strings.TrimSpace(rawValue))
		if err != nil {
			return applicationConfig{}, fmt.Errorf("line %d: value must be a quoted string", lineNumber)
		}
		switch section {
		case "storage":
			if key == "path" {
				config.StoragePath = strings.TrimSpace(value)
			}
		default:
			if strings.HasPrefix(section, "page_types.") {
				id := PageType(strings.TrimPrefix(section, "page_types."))
				entry := &pageTypes[pageTypeIndexes[id]]
				switch key {
				case "label", "label_en":
					entry.Label = value
				case "description", "description_en":
					entry.Description = value
				case "color":
					entry.Color = value
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return applicationConfig{}, fmt.Errorf("read config: %w", err)
	}
	if len(pageTypes) > 0 {
		validated, err := validatePageTypes(pageTypes)
		if err != nil {
			return applicationConfig{}, err
		}
		config.PageTypes = validated
	}
	return config, nil
}

func persistApplicationConfig(path string, config applicationConfig) error {
	pageTypes, err := validatePageTypes(config.PageTypes)
	if err != nil {
		return err
	}
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
	var contents strings.Builder
	contents.WriteString("# Mnemosys application configuration.\n\n[storage]\npath = ")
	contents.WriteString(strconv.Quote(config.StoragePath))
	for _, value := range pageTypes {
		contents.WriteString("\n[page_types.")
		contents.WriteString(string(value.ID))
		contents.WriteString("]\nlabel = ")
		contents.WriteString(strconv.Quote(value.Label))
		contents.WriteString("\ndescription = ")
		contents.WriteString(strconv.Quote(value.Description))
		contents.WriteString("\ncolor = ")
		contents.WriteString(strconv.Quote(value.Color))
		contents.WriteString("\n")
	}
	if _, err := temporary.WriteString(contents.String()); err != nil {
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
