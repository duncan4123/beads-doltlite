package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/metrics"
)

var (
	backendInstallCommand string
	backendInstallArgs    []string
	backendInstallTrace   string
)

var backendCmd = &cobra.Command{
	Use:     "backend",
	GroupID: "setup",
	Short:   "Manage storage backend configuration",
	Long: `Manage storage backend configuration stored in .beads/metadata.json.

Backend plugins are external processes launched by bd. Installing a plugin
records the backend name, plugin executable, and plugin arguments in metadata so
future bd commands open the backend through the plugin-process adapter.`,
}

var backendInstallCmd = &cobra.Command{
	Use:           "install <backend>",
	Short:         "Install an external backend plugin for this workspace",
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(_ *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("backend-install")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		beadsDir := beads.FindBeadsDir()
		if beadsDir == "" {
			return HandleError("%s", activeWorkspaceNotFoundError())
		}
		result, err := installBackendPlugin(beadsDir, backendInstallInput{
			Backend: args[0],
			Command: backendInstallCommand,
			Args:    backendInstallArgs,
			Trace:   backendInstallTrace,
		})
		if err != nil {
			return HandleError("%v", err)
		}
		if jsonOutput {
			return outputJSON(result)
		}
		fmt.Printf("Installed backend plugin %q\n", result.Backend)
		fmt.Printf("  command: %s\n", result.Command)
		if len(result.Args) > 0 {
			fmt.Printf("  args: %s\n", strings.Join(result.Args, " "))
		}
		fmt.Printf("  metadata: %s\n", result.MetadataPath)
		return nil
	},
}

type backendInstallInput struct {
	Backend string
	Command string
	Args    []string
	Trace   string
}

type backendInstallResult struct {
	Backend      string   `json:"backend"`
	Command      string   `json:"command"`
	Args         []string `json:"args,omitempty"`
	MetadataPath string   `json:"metadata_path"`
}

func installBackendPlugin(beadsDir string, input backendInstallInput) (*backendInstallResult, error) {
	backend := strings.ToLower(strings.TrimSpace(input.Backend))
	if backend == "" {
		return nil, fmt.Errorf("backend name is required")
	}
	command := strings.TrimSpace(input.Command)
	if command == "" {
		return nil, fmt.Errorf("--command is required")
	}
	resolvedCommand, err := resolveBackendPluginCommand(command)
	if err != nil {
		return nil, err
	}
	args := append([]string(nil), input.Args...)
	if trace := strings.TrimSpace(input.Trace); trace != "" {
		args = append([]string{"--trace", trace}, args...)
	}
	if len(args) == 0 {
		args = []string{"serve"}
	} else if !backendPluginArgsIncludeServe(args) {
		args = append(args, "serve")
	}

	cfg, err := configfile.Load(beadsDir)
	if err != nil {
		return nil, fmt.Errorf("loading metadata.json: %w", err)
	}
	if cfg == nil {
		cfg = configfile.DefaultConfig()
	}
	cfg.Backend = backend
	if strings.TrimSpace(cfg.Database) == "" || cfg.Database == "beads.db" {
		cfg.Database = backend
	}
	cfg.BackendPluginCommand = resolvedCommand
	cfg.BackendPluginArgs = args
	if strings.TrimSpace(cfg.DoltDatabase) == "" {
		cfg.DoltDatabase = configfile.DefaultDoltDatabase
	}
	if err := cfg.Save(beadsDir); err != nil {
		return nil, fmt.Errorf("saving metadata.json: %w", err)
	}

	return &backendInstallResult{
		Backend:      backend,
		Command:      resolvedCommand,
		Args:         args,
		MetadataPath: configfile.ConfigPath(beadsDir),
	}, nil
}

func resolveBackendPluginCommand(command string) (string, error) {
	if strings.ContainsRune(command, os.PathSeparator) || filepath.IsAbs(command) {
		abs, err := filepath.Abs(command)
		if err != nil {
			return "", fmt.Errorf("resolving plugin command: %w", err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("plugin command is not accessible: %w", err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("plugin command is a directory: %s", abs)
		}
		if info.Mode()&0o111 == 0 {
			return "", fmt.Errorf("plugin command is not executable: %s", abs)
		}
		return abs, nil
	}
	if resolved, err := execLookPath(command); err == nil {
		return resolved, nil
	}
	return "", fmt.Errorf("plugin command %q not found in PATH", command)
}

var execLookPath = func(file string) (string, error) {
	return exec.LookPath(file)
}

func backendPluginArgsIncludeServe(args []string) bool {
	for _, arg := range args {
		if arg == "serve" {
			return true
		}
	}
	return false
}

func init() {
	backendInstallCmd.Flags().StringVar(&backendInstallCommand, "command", "", "Plugin executable path or PATH command")
	backendInstallCmd.Flags().StringArrayVar(&backendInstallArgs, "arg", nil, "Argument to pass to the plugin process before serve; repeatable")
	backendInstallCmd.Flags().StringVar(&backendInstallTrace, "trace", "", "Trace JSONL path; prepends --trace <path> to plugin args")
	backendCmd.AddCommand(backendInstallCmd)
	rootCmd.AddCommand(backendCmd)
}
