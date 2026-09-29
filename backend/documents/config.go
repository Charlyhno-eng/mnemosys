package documents

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type applicationConfig struct {
	StoragePath     string
	Profile         ProfileSettings
	AIPermissions   Permissions
	Profiles        []SavedProfile
	ActiveProfileID string
}

func defaultProfile() ProfileSettings {
	return ProfileSettings{Type: ProfileHuman}
}

func defaultAIPermissions() Permissions {
	return Permissions{View: true}
}

func validateProfile(profile ProfileSettings) (ProfileSettings, error) {
	profile.FirstName = strings.TrimSpace(profile.FirstName)
	profile.LastName = strings.TrimSpace(profile.LastName)
	profile.Team = strings.TrimSpace(profile.Team)
	profile.Name = strings.TrimSpace(profile.Name)
	if profile.Type != ProfileHuman && profile.Type != ProfileAI {
		return ProfileSettings{}, fmt.Errorf("%w: profile type must be human or ai", ErrInvalidSettings)
	}
	if len(profile.FirstName) > 100 || len(profile.LastName) > 100 || len(profile.Team) > 100 || len(profile.Name) > 100 || strings.ContainsAny(profile.FirstName+profile.LastName+profile.Team+profile.Name, "\r\n") {
		return ProfileSettings{}, fmt.Errorf("%w: invalid profile name", ErrInvalidSettings)
	}
	return profile, nil
}

func parseApplicationConfig(data []byte) (applicationConfig, error) {
	config := applicationConfig{AIPermissions: defaultAIPermissions()}
	section := ""
	legacyProfile := false
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
		key, rawValue, ok := strings.Cut(line, "=")
		if !ok {
			return applicationConfig{}, fmt.Errorf("line %d: expected key = value", lineNumber)
		}
		key = strings.TrimSpace(key)
		switch section {
		case "storage":
			if key == "path" {
				value, err := strconv.Unquote(strings.TrimSpace(rawValue))
				if err != nil {
					return applicationConfig{}, fmt.Errorf("line %d: storage path must be a quoted string", lineNumber)
				}
				config.StoragePath = strings.TrimSpace(value)
			}
		case "profile":
			legacyProfile = true
			switch key {
			case "type", "first_name", "last_name", "team":
				value, err := strconv.Unquote(strings.TrimSpace(rawValue))
				if err != nil {
					return applicationConfig{}, fmt.Errorf("line %d: profile value must be a quoted string", lineNumber)
				}
				switch key {
				case "type":
					config.Profile.Type = ProfileType(value)
				case "first_name":
					config.Profile.FirstName = value
				case "last_name":
					config.Profile.LastName = value
				case "team":
					config.Profile.Team = value
				}
			}
		case "ai_permissions":
			value, err := strconv.ParseBool(strings.TrimSpace(rawValue))
			if err != nil {
				return applicationConfig{}, fmt.Errorf("line %d: permission value must be a boolean", lineNumber)
			}
			switch key {
			case "view":
				config.AIPermissions.View = value
			case "create":
				config.AIPermissions.Create = value
			case "edit":
				config.AIPermissions.Edit = value
			case "delete":
				config.AIPermissions.Delete = value
			}
		case "profiles":
			if key == "active_id" {
				value, err := strconv.Unquote(strings.TrimSpace(rawValue))
				if err != nil {
					return applicationConfig{}, fmt.Errorf("line %d: active profile ID must be quoted", lineNumber)
				}
				config.ActiveProfileID = value
			}
		default:
			if strings.HasPrefix(section, "profiles.") {
				id := strings.TrimPrefix(section, "profiles.")
				if !validProfileID(id) {
					return applicationConfig{}, fmt.Errorf("line %d: invalid profile ID", lineNumber)
				}
				index := -1
				for i := range config.Profiles {
					if config.Profiles[i].ID == id {
						index = i
						break
					}
				}
				if index < 0 {
					config.Profiles = append(config.Profiles, SavedProfile{ID: id})
					index = len(config.Profiles) - 1
				}
				entry := &config.Profiles[index]
				raw := strings.TrimSpace(rawValue)
				switch key {
				case "view", "create", "edit", "delete":
					value, err := strconv.ParseBool(raw)
					if err != nil {
						return applicationConfig{}, fmt.Errorf("line %d: permission must be boolean", lineNumber)
					}
					switch key {
					case "view":
						entry.Permissions.View = value
					case "create":
						entry.Permissions.Create = value
					case "edit":
						entry.Permissions.Edit = value
					case "delete":
						entry.Permissions.Delete = value
					}
				case "type", "first_name", "last_name", "name", "team":
					value, err := strconv.Unquote(raw)
					if err != nil {
						return applicationConfig{}, fmt.Errorf("line %d: profile value must be quoted", lineNumber)
					}
					switch key {
					case "type":
						entry.Type = ProfileType(value)
					case "first_name":
						entry.FirstName = value
					case "last_name":
						entry.LastName = value
					case "name":
						entry.Name = value
					case "team":
						entry.Team = value
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return applicationConfig{}, fmt.Errorf("read config: %w", err)
	}
	// The tracked starter config contained an unnamed Human placeholder. It is
	// not a user-created profile and must not populate a new installation.
	if legacyProfile && config.Profile == defaultProfile() && config.AIPermissions == defaultAIPermissions() && config.StoragePath == "" {
		legacyProfile = false
		config.Profile = ProfileSettings{}
	}
	if len(config.Profiles) == 0 && legacyProfile {
		validated, err := validateProfile(config.Profile)
		if err != nil {
			return applicationConfig{}, err
		}
		if validated.Type == ProfileAI && validated.Name == "" {
			validated.Name = strings.TrimSpace(validated.FirstName + " " + validated.LastName)
			validated.FirstName, validated.LastName = "", ""
			if validated.Name == "" {
				validated.Name = "AI"
			}
		}
		config.Profile = validated
		config.Profiles = []SavedProfile{{ID: "legacy", ProfileSettings: validated, Permissions: config.AIPermissions}}
		config.ActiveProfileID = "legacy"
	}
	for i := range config.Profiles {
		validate := validateNewProfile
		if config.Profiles[i].ID == "legacy" {
			validate = validateProfile
		}
		validated, err := validate(config.Profiles[i].ProfileSettings)
		if err != nil {
			return applicationConfig{}, err
		}
		config.Profiles[i].ProfileSettings = validated
		if config.Profiles[i].ID != "legacy" {
			if err := validateProfilePermissions(validated, config.Profiles[i].Permissions); err != nil {
				return applicationConfig{}, err
			}
		}
	}
	if len(config.Profiles) > 0 && config.ActiveProfileID != "" {
		found := false
		for _, entry := range config.Profiles {
			if entry.ID == config.ActiveProfileID {
				config.Profile = entry.ProfileSettings
				config.AIPermissions = entry.Permissions
				found = true
				break
			}
		}
		if !found {
			return applicationConfig{}, fmt.Errorf("%w: active profile does not exist", ErrInvalidSettings)
		}
	} else if config.ActiveProfileID != "" {
		return applicationConfig{}, fmt.Errorf("%w: active profile does not exist", ErrInvalidSettings)
	}
	return config, nil
}

func validProfileID(id string) bool {
	if id == "legacy" {
		return true
	}
	if len(id) != 36 {
		return false
	}
	for i, char := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if char != '-' {
				return false
			}
			continue
		}
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

func persistApplicationConfig(path string, config applicationConfig) error {
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
	contents.WriteString("\n\n[profiles]\nactive_id = ")
	contents.WriteString(strconv.Quote(config.ActiveProfileID))
	for _, entry := range config.Profiles {
		if !validProfileID(entry.ID) {
			return fmt.Errorf("%w: invalid profile ID", ErrInvalidSettings)
		}
		profile, err := validateProfile(entry.ProfileSettings)
		if err != nil {
			return err
		}
		contents.WriteString("\n\n[profiles." + entry.ID + "]\ntype = " + strconv.Quote(string(profile.Type)))
		contents.WriteString("\nfirst_name = " + strconv.Quote(profile.FirstName))
		contents.WriteString("\nlast_name = " + strconv.Quote(profile.LastName))
		contents.WriteString("\nname = " + strconv.Quote(profile.Name))
		contents.WriteString("\nteam = " + strconv.Quote(profile.Team))
		contents.WriteString("\nview = " + strconv.FormatBool(entry.Permissions.View))
		contents.WriteString("\ncreate = " + strconv.FormatBool(entry.Permissions.Create))
		contents.WriteString("\nedit = " + strconv.FormatBool(entry.Permissions.Edit))
		contents.WriteString("\ndelete = " + strconv.FormatBool(entry.Permissions.Delete))
	}
	contents.WriteString("\n")
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
