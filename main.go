package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
)

// Config represents our application configuration
type Config struct {
	Servers map[string]ServerConfig `json:"servers"`
}

// ServerConfig holds configuration for each server
type ServerConfig struct {
	Script      string            `json:"script,omitempty"`      // Direct script command to run
	ScriptPath  string            `json:"script_path,omitempty"` // For backward compatibility
	ScriptArgs  []string          `json:"script_args,omitempty"` // For backward compatibility
	EnvVars     map[string]string `json:"env_vars,omitempty"`    // Still useful for both script types
	Description string            `json:"description,omitempty"`
}

// AppState represents the different states our app can be in
type AppState int

const (
	StateVaultTokenInput AppState = iota
	StateServerSelection
	StateScriptOutput
)

// ServerModel represents the state of our application
type ServerModel struct {
	config          Config
	serverNames     []string
	cursor          int
	selected        string
	state           AppState
	runOutput       string
	hasError        bool
	errorMessage    string
	vaultToken      string
	vaultTokenInput string
	pasteError      string
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
					m.state = StateServerSelection
					return m, nil
				}

			case "ctrl+v": // Handle paste
				text, err := clipboard.ReadAll()
				if err != nil {
					m.pasteError = fmt.Sprintf("Failed to paste: %v", err)
				} else {
					m.vaultTokenInput += text
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

		case StateServerSelection:
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "up", "k":
				if m.cursor > 0 {
					m.cursor--
				}
			case "down", "j":
				if m.cursor < len(m.serverNames)-1 {
					m.cursor++
				}
			case "enter":
				m.selected = m.serverNames[m.cursor]
				m.state = StateScriptOutput
				return m, runServerScript(m.selected, m.config.Servers[m.selected], m.vaultToken)
			}

		case StateScriptOutput:
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "b":
				// Go back to selection
				m.state = StateServerSelection
				m.runOutput = ""
				m.hasError = false
				return m, nil
			}
		}

	case vaultTokenMsg:
		m.vaultToken = string(msg)
		m.state = StateServerSelection
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

		// Add paste instructions and error (if any)
		s += "\n\n"
		s += infoStyle.Render("Type or press Ctrl+V to paste your token, then press Enter")

		if m.pasteError != "" {
			s += "\n" + errorStyle.Render(m.pasteError)
		}

		return s

	case StateServerSelection:
		// Selection view
		s := titleStyle.Render("Server Selection") + "\n\n"
		s += "Select a server region to run script:\n\n"

		for i, serverName := range m.serverNames {
			cursor := " "
			if m.cursor == i {
				cursor = ">"
			}

			serverInfo := serverName
			if desc := m.config.Servers[serverName].Description; desc != "" {
				serverInfo = fmt.Sprintf("%s - %s", serverName, desc)
			}

			if m.cursor == i {
				s += selectedItemStyle.Render(fmt.Sprintf("%s %s", cursor, serverInfo)) + "\n"
			} else {
				s += itemStyle.Render(fmt.Sprintf("%s %s", cursor, serverInfo)) + "\n"
			}
		}

		s += "\n"
		s += infoStyle.Render("Use arrow keys or j/k to navigate, enter to select, q to quit")
		return s

	case StateScriptOutput:
		// Result view
		s := titleStyle.Render("Script Output") + "\n\n"
		s += fmt.Sprintf("Running script for region: %s\n\n", m.selected)

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

	err = json.Unmarshal(data, &config)
	if err != nil {
		return config, fmt.Errorf("could not parse config file: %v", err)
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
		return "./server-cli-config.json"
	}
	return filepath.Join(homeDir, ".config", "server-cli", "config.json")
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

	// Extract server names
	var serverNames []string
	for name := range config.Servers {
		serverNames = append(serverNames, name)
	}

	if len(serverNames) == 0 {
		return ServerModel{errorMessage: "No servers defined in configuration"}, nil
	}

	return ServerModel{
		config:          config,
		serverNames:     serverNames,
		state:           StateVaultTokenInput, // Start in token input state
		vaultTokenInput: "",
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
