package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"
)

// Define some basic styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).
			Padding(0, 1)

	itemStyle         = lipgloss.NewStyle().PaddingLeft(2)
	selectedItemStyle = lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color("#7D56F4"))
	infoStyle         = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("#666666"))
	errorStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	promptStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#4CAF50"))
	inputStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#2196F3"))
	categoryStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF9800"))
)

// Config represents our application configuration
type Config struct {
	Categories map[string]CategoryConfig `yaml:"categories"`
}

// CategoryConfig represents a category (logging or metrics)
type CategoryConfig struct {
	Description string                  `yaml:"description,omitempty"`
	Servers     map[string]ServerConfig `yaml:"servers"`
}

// ServerConfig holds configuration for each server
type ServerConfig struct {
	Script      string            `yaml:"script,omitempty"`      // Direct script command to run
	ScriptPath  string            `yaml:"script_path,omitempty"` // For backward compatibility
	ScriptArgs  []string          `yaml:"script_args,omitempty"` // For backward compatibility
	EnvVars     map[string]string `yaml:"env_vars,omitempty"`    // Still useful for both script types
	Description string            `yaml:"description,omitempty"`
}

// AppState represents the different states our app can be in
type AppState int

const (
	StateVaultTokenInput AppState = iota
	StateCategorySelection
	StateServerSelection
	StateScriptOutput
)

// ServerModel represents the state of our application
type ServerModel struct {
	config           Config
	categoryNames    []string
	serverNames      []string
	categoryCursor   int
	serverCursor     int
	selectedCategory string
	selectedServer   string
	state            AppState
	runOutput        string
	hasError         bool
	errorMessage     string
	vaultToken       string
	vaultTokenInput  string
	pasteError       string
}

// Init initializes the model
func (m ServerModel) Init() tea.Cmd {
	return checkVaultToken
}

// Check if VAULT_TOKEN is set
func checkVaultToken() tea.Msg {
	token := os.Getenv("VAULT_TOKEN")
	if token != "" {
		return vaultTokenMsg(token)
	}
	return vaultTokenMissingMsg{}
}

// Attempt to paste from clipboard
func pasteFromClipboard(model ServerModel) (ServerModel, error) {
	text, err := clipboard.ReadAll()
	if err != nil {
		model.pasteError = fmt.Sprintf("Failed to paste: %v", err)
		return model, err
	}
	model.vaultTokenInput += text
	return model, nil
}

// Update handles all the updates to the model
func (m ServerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.state {
		case StateVaultTokenInput:
			m.pasteError = "" // Clear any paste error on keypress

			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit

			case "enter":
				if m.vaultTokenInput != "" {
					// Set the environment variable
					os.Setenv("VAULT_TOKEN", m.vaultTokenInput)
					m.vaultToken = m.vaultTokenInput
					m.state = StateCategorySelection
					return m, nil
				}

			case "ctrl+v", "cmd+v": // Handle paste for both Windows/Linux and macOS
				var err error
				m, err = pasteFromClipboard(m)
				if err != nil {
					// Error is already set in the model
					return m, nil
				}

			case "backspace":
				if len(m.vaultTokenInput) > 0 {
					m.vaultTokenInput = m.vaultTokenInput[:len(m.vaultTokenInput)-1]
				}

			default:
				// Add character to input (except for control keys)
				if len(msg.String()) == 1 {
					m.vaultTokenInput += msg.String()
				}
			}

		case StateCategorySelection:
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "up", "k":
				if m.categoryCursor > 0 {
					m.categoryCursor--
				}
			case "down", "j":
				if m.categoryCursor < len(m.categoryNames)-1 {
					m.categoryCursor++
				}
			case "enter":
				m.selectedCategory = m.categoryNames[m.categoryCursor]

				// Update server names based on selected category
				var serverNames []string
				for name := range m.config.Categories[m.selectedCategory].Servers {
					serverNames = append(serverNames, name)
				}
				sort.Strings(serverNames)
				m.serverNames = serverNames
				m.serverCursor = 0 // Reset server cursor

				m.state = StateServerSelection
			}

		case StateServerSelection:
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "up", "k":
				if m.serverCursor > 0 {
					m.serverCursor--
				}
			case "down", "j":
				if m.serverCursor < len(m.serverNames)-1 {
					m.serverCursor++
				}
			case "enter":
				m.selectedServer = m.serverNames[m.serverCursor]
				m.state = StateScriptOutput

				// Get the server config from the selected category
				serverConfig := m.config.Categories[m.selectedCategory].Servers[m.selectedServer]
				return m, runServerScript(m.selectedServer, serverConfig, m.vaultToken)

			case "b", "backspace", "esc":
				// Go back to category selection
				m.state = StateCategorySelection
				return m, nil
			}

		case StateScriptOutput:
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "b", "backspace", "esc":
				// Go back to server selection
				m.state = StateServerSelection
				m.runOutput = ""
				m.hasError = false
				return m, nil
			}
		}

	case vaultTokenMsg:
		m.vaultToken = string(msg)
		m.state = StateCategorySelection
		return m, nil

	case vaultTokenMissingMsg:
		m.state = StateVaultTokenInput
		return m, nil

	case scriptOutputMsg:
		m.runOutput = string(msg)
		return m, nil

	case scriptErrorMsg:
		m.runOutput = string(msg)
		m.hasError = true
		return m, nil

	case configErrorMsg:
		m.errorMessage = string(msg)
		return m, tea.Quit
	}

	return m, nil
}

// View renders the UI based on the model state
func (m ServerModel) View() string {
	if m.errorMessage != "" {
		return errorStyle.Render(fmt.Sprintf("Configuration Error: %s", m.errorMessage))
	}

	switch m.state {
	case StateVaultTokenInput:
		s := titleStyle.Render("Vault Authentication") + "\n\n"
		s += promptStyle.Render("VAULT_TOKEN environment variable is not set.") + "\n"
		s += promptStyle.Render("Please enter your Vault token:") + "\n\n"

		// Show asterisks instead of the actual token for security
		maskedInput := strings.Repeat("*", len(m.vaultTokenInput))
		s += inputStyle.Render("> " + maskedInput)

		// Add paste instructions with OS-aware shortcuts
		s += "\n\n"
		s += infoStyle.Render("Type or paste (⌘V on macOS, Ctrl+V on Windows/Linux) your token, then press Enter")

		if m.pasteError != "" {
			s += "\n" + errorStyle.Render(m.pasteError)
		}

		return s

	case StateCategorySelection:
		// Category selection view
		s := titleStyle.Render("Category Selection") + "\n\n"
		s += "Select a category:\n\n"

		for i, categoryName := range m.categoryNames {
			cursor := " "
			if m.categoryCursor == i {
				cursor = ">"
			}

			categoryInfo := categoryName
			if desc := m.config.Categories[categoryName].Description; desc != "" {
				categoryInfo = fmt.Sprintf("%s - %s", categoryName, desc)
			}

			if m.categoryCursor == i {
				s += selectedItemStyle.Render(fmt.Sprintf("%s %s", cursor, categoryInfo)) + "\n"
			} else {
				s += itemStyle.Render(fmt.Sprintf("%s %s", cursor, categoryInfo)) + "\n"
			}
		}

		s += "\n"
		s += infoStyle.Render("Use arrow keys or j/k to navigate, enter to select, q to quit")
		return s

	case StateServerSelection:
		// Server selection view
		s := titleStyle.Render("Server Selection") + "\n\n"
		s += categoryStyle.Render(fmt.Sprintf("Category: %s", m.selectedCategory)) + "\n\n"
		s += "Select a server region to run script:\n\n"

		for i, serverName := range m.serverNames {
			cursor := " "
			if m.serverCursor == i {
				cursor = ">"
			}

			serverInfo := serverName
			if desc := m.config.Categories[m.selectedCategory].Servers[serverName].Description; desc != "" {
				serverInfo = fmt.Sprintf("%s - %s", serverName, desc)
			}

			if m.serverCursor == i {
				s += selectedItemStyle.Render(fmt.Sprintf("%s %s", cursor, serverInfo)) + "\n"
			} else {
				s += itemStyle.Render(fmt.Sprintf("%s %s", cursor, serverInfo)) + "\n"
			}
		}

		s += "\n"
		s += infoStyle.Render("Use arrow keys or j/k to navigate, enter to select, b to go back, q to quit")
		return s

	case StateScriptOutput:
		// Result view
		s := titleStyle.Render("Script Output") + "\n\n"
		s += categoryStyle.Render(fmt.Sprintf("Category: %s", m.selectedCategory)) + "\n"
		s += fmt.Sprintf("Server: %s\n\n", m.selectedServer)

		if m.runOutput != "" {
			if m.hasError {
				s += errorStyle.Render("Error: " + m.runOutput)
			} else {
				s += m.runOutput
			}
		} else {
			s += "Running script..."
		}

		s += "\n\n"
		s += infoStyle.Render("Press 'b' to go back to server selection, q to quit")
		return s
	}

	return "Unknown state"
}

// Custom messages for our commands
type scriptOutputMsg string
type scriptErrorMsg string
type configErrorMsg string
type vaultTokenMsg string
type vaultTokenMissingMsg struct{}

// LoadConfig loads the configuration from a file
func LoadConfig(path string) (Config, error) {
	var config Config

	data, err := ioutil.ReadFile(path)
	if err != nil {
		return config, fmt.Errorf("could not read config file: %v", err)
	}

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return config, fmt.Errorf("could not parse YAML config file: %v", err)
	}

	return config, nil
}

// Function to run a script for a server
func runServerScript(server string, config ServerConfig, vaultToken string) tea.Cmd {
	return func() tea.Msg {
		// Check if we have a direct script to run
		if config.Script != "" {
			// Create environment variables string for server and vault token
			envVarsStr := fmt.Sprintf("export SERVER_REGION=%s; export VAULT_TOKEN=%s; ",
				server, vaultToken)

			// Add custom environment variables from config
			for key, value := range config.EnvVars {
				envVarsStr += fmt.Sprintf("export %s=%s; ", key, value)
			}

			// Combine env vars with script
			fullCommand := envVarsStr + config.Script

			// Run the command
			cmd := exec.Command("bash", "-c", fullCommand)
			output, err := cmd.CombinedOutput()
			if err != nil {
				return scriptErrorMsg(fmt.Sprintf("%v\n%s", err, output))
			}
			return scriptOutputMsg(output)
		} else if config.ScriptPath != "" {
			// For backward compatibility with the old script_path method
			cmd := exec.Command(config.ScriptPath, config.ScriptArgs...)

			// Set environment variables
			env := os.Environ()
			env = append(env, fmt.Sprintf("SERVER_REGION=%s", server))
			env = append(env, fmt.Sprintf("VAULT_TOKEN=%s", vaultToken))

			for key, value := range config.EnvVars {
				env = append(env, fmt.Sprintf("%s=%s", key, value))
			}

			cmd.Env = env

			// Run the command
			output, err := cmd.CombinedOutput()
			if err != nil {
				return scriptErrorMsg(fmt.Sprintf("%v\n%s", err, output))
			}
			return scriptOutputMsg(output)
		} else {
			return scriptErrorMsg("No script or script_path defined for this server")
		}
	}
}

func getDefaultConfigPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "./server-cli-config.yaml"
	}
	return filepath.Join(homeDir, ".config", "server-cli", "config.yaml")
}

// Initial checks and config loading
func initialSetup() (ServerModel, tea.Cmd) {
	// Try to load config
	configPath := os.Getenv("SERVER_CLI_CONFIG")
	if configPath == "" {
		configPath = getDefaultConfigPath()
	}

	config, err := LoadConfig(configPath)
	if err != nil {
		return ServerModel{errorMessage: err.Error()}, nil
	}

	// Extract category names
	var categoryNames []string
	for name := range config.Categories {
		categoryNames = append(categoryNames, name)
	}

	if len(categoryNames) == 0 {
		return ServerModel{errorMessage: "No categories defined in configuration"}, nil
	}

	// Sort categories alphabetically for consistent display
	sort.Strings(categoryNames)

	return ServerModel{
		config:          config,
		categoryNames:   categoryNames,
		state:           StateVaultTokenInput, // Start in token input state
		vaultTokenInput: "",
		categoryCursor:  0,
		serverCursor:    0,
	}, nil
}

func main() {
	model, cmd := initialSetup()

	p := tea.NewProgram(model)
	p.EnterAltScreen()
	defer p.ExitAltScreen()

	if cmd != nil {
		p.Send(cmd())
	}

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}
}
