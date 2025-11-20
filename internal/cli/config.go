package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage eulix configuration",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Configuration management coming soon!")
	},
}

func initializeProject() error {
	// Create .eulix directory
	eulixDir := ".eulix"
	if err := os.MkdirAll(eulixDir, 0755); err != nil {
		return fmt.Errorf("failed to create .eulix directory: %w", err)
	}

	// Create .euignore file
	euignorePath := ".euignore"
	if _, err := os.Stat(euignorePath); os.IsNotExist(err) {
		defaultIgnore := `# Eulix ignore patterns
node_modules/
.git/
*.test.go
vendor/
dist/
build/
`
		if err := os.WriteFile(euignorePath, []byte(defaultIgnore), 0644); err != nil {
			return fmt.Errorf("failed to create .euignore: %w", err)
		}
	}

	// Create eulix.toml if it doesn't exist
	configPath := "eulix.toml"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		defaultConfig := `# Eulix Configuration

[project]
path = "."

[parser]
threads = 4

[embeddings]
model = "BAAI/bge-small-en-v1.5"
backend = "auto"
dimension = 384

[llm]
local = true
provider = "ollama"
model = "llama3.2:3b"
max_tokens = 8192
temperature = 0.7
baseURL = "http://localhost:11434"

# To use Anthropic Claude instead, change to:
# local = false
# provider = "anthropic"
# model = "claude-3-5-sonnet-20241022"
# api_key = ""  # or set ANTHROPIC_API_KEY environment variable

[cache]
[cache.redis]
enabled = false
url = "redis://localhost:6379"
ttl_hours = 6

[cache.sql]
enabled = true
driver = "sqlite"
dsn = ".eulix/history.db"

[checksum]
change_threshold = 0.10
force_reanalyze_threshold = 0.30
`
		if err := os.WriteFile(configPath, []byte(defaultConfig), 0644); err != nil {
			return fmt.Errorf("failed to create config: %w", err)
		}
	}

	fmt.Println("✨ Eulix initialized successfully!")
	fmt.Println()
	fmt.Println("Created:")
	fmt.Println("  - .eulix/       (knowledge base directory)")
	fmt.Println("  - .euignore     (ignore patterns)")
	fmt.Println("  - eulix.toml    (configuration)")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Edit eulix.toml to configure your setup")
	fmt.Println("  2. Run 'eulix analyze' to analyze your codebase")
	fmt.Println("  3. Run 'eulix chat' to start querying")

	return nil
}
