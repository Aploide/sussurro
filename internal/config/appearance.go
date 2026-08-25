package config

import (
	"fmt"
	"os"
)

// Theme controls whether app surfaces follow the desktop colour scheme or use
// an explicit palette.
type Theme string

const (
	ThemeSystem Theme = "system"
	ThemeLight  Theme = "light"
	ThemeDark   Theme = "dark"
)

// AppearanceConfig holds settings shared by the Settings window and native
// overlay.
type AppearanceConfig struct {
	Theme Theme `mapstructure:"theme"`
}

func (a *AppearanceConfig) Normalize() {
	if a.Theme == "" {
		a.Theme = ThemeSystem
	}
}

func (a AppearanceConfig) Validate() error {
	switch a.Theme {
	case ThemeSystem, ThemeLight, ThemeDark:
		return nil
	default:
		return fmt.Errorf("appearance.theme must be one of %q, %q, or %q, got %q",
			ThemeSystem, ThemeLight, ThemeDark, a.Theme)
	}
}

// SaveTheme atomically persists appearance.theme in the file that supplied
// cfg. It does not mutate cfg, so callers can update live state only after the
// write succeeds.
func SaveTheme(cfg *Config, theme Theme) error {
	appearance := AppearanceConfig{Theme: theme}
	if err := appearance.Validate(); err != nil {
		return err
	}

	configSaveMu.Lock()
	defer configSaveMu.Unlock()

	configFile, err := configPath(cfg)
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("cannot read config file: %w", err)
	}
	updated, err := SetNestedValue(string(data), "appearance.theme", YAMLString(string(theme)))
	if err != nil {
		return fmt.Errorf("set appearance.theme: %w", err)
	}
	if err := writeConfigAtomically(configFile, []byte(updated)); err != nil {
		return fmt.Errorf("write config atomically: %w", err)
	}
	return nil
}
