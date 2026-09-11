package config

// SourcePath returns the configuration file selected by LoadConfig.
func (c *Config) SourcePath() string {
	if c == nil {
		return ""
	}
	return c.sourcePath
}
