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
	StoragePath   string
	Profile       ProfileSettings
	AIPermissions Permissions
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
	if profile.Type != ProfileHuman && profile.Type != ProfileAI {
		return ProfileSettings{}, fmt.Errorf("%w: profile type must be human or ai", ErrInvalidSettings)
	}
	if len(profile.FirstName) > 100 || len(profile.LastName) > 100 || strings.ContainsAny(profile.FirstName+profile.LastName, "\r\n") {
		return ProfileSettings{}, fmt.Errorf("%w: invalid profile name", ErrInvalidSettings)
	}
	return profile, nil
}

func parseApplicationConfig(data []byte) (applicationConfig, error) {
	config := applicationConfig{Profile: defaultProfile(), AIPermissions: defaultAIPermissions()}
	section := ""
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
			switch key {
			case "type", "first_name", "last_name":
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
		}
	}
	if err := scanner.Err(); err != nil {
		return applicationConfig{}, fmt.Errorf("read config: %w", err)
	}
	validated, err := validateProfile(config.Profile)
	if err != nil {
		return applicationConfig{}, err
	}
	config.Profile = validated
	return config, nil
}

func persistApplicationConfig(path string, config applicationConfig) error {
	profile, err := validateProfile(config.Profile)
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
	contents.WriteString("\n\n[profile]\ntype = ")
	contents.WriteString(strconv.Quote(string(profile.Type)))
	contents.WriteString("\nfirst_name = ")
	contents.WriteString(strconv.Quote(profile.FirstName))
	contents.WriteString("\nlast_name = ")
	contents.WriteString(strconv.Quote(profile.LastName))
	contents.WriteString("\n\n[ai_permissions]\nview = ")
	contents.WriteString(strconv.FormatBool(config.AIPermissions.View))
	contents.WriteString("\ncreate = ")
	contents.WriteString(strconv.FormatBool(config.AIPermissions.Create))
	contents.WriteString("\nedit = ")
	contents.WriteString(strconv.FormatBool(config.AIPermissions.Edit))
	contents.WriteString("\ndelete = ")
	contents.WriteString(strconv.FormatBool(config.AIPermissions.Delete))
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
