package plugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
)

// HandlePluginCommand dispatches `late plugin <subcommand>` to the appropriate handler.
// Returns true if the command was handled (caller should exit), false if the caller
// should continue (e.g. the plugin manager needs to bootstrap first).
func HandlePluginCommand(pm *PluginManager, args []string) bool {
	if len(args) == 0 {
		printPluginUsage()
		return true
	}

	switch args[0] {
	case "list", "ls":
		handlePluginList(pm)
		return true
	case "install", "i":
		handlePluginInstall(pm, args[1:])
		return true
	case "remove", "rm", "uninstall":
		handlePluginRemove(pm, args[1:])
		return true
	case "link":
		handlePluginLink(pm, args[1:])
		return true
	case "update":
		handlePluginUpdate(pm, args[1:])
		return true
	case "enable":
		handlePluginEnable(pm, args[1:], true)
		return true
	case "disable":
		handlePluginEnable(pm, args[1:], false)
		return true
	default:
		fmt.Fprintf(os.Stderr, "Unknown plugin command: %s\n\n", args[0])
		printPluginUsage()
		return true
	}
}

func printPluginUsage() {
	fmt.Fprintf(os.Stderr, `Usage: late plugin <command> [args...]

Commands:
  list, ls              List installed plugins
  install, i <src>      Install a plugin (npm package, git url, or local path)
  remove, rm <name>     Remove a plugin
  link <path>           Link a local directory as a plugin (dev mode)
  update [name]         Update plugins (all or specific)
  enable <name>         Enable a plugin
  disable <name>        Disable a plugin

Examples:
  late plugin install @late/plugin-graph-rag
  late plugin install https://github.com/user/late-plugin.git
  late plugin install github:user/late-plugin
  late plugin link ./my-plugin
  late plugin list
  late plugin remove @late/plugin-graph-rag
`)
}

// handlePluginList displays all installed plugins.
func handlePluginList(pm *PluginManager) {
	plugins := pm.All()
	if len(plugins) == 0 {
		fmt.Println("No plugins installed.")
		fmt.Println("Run 'late plugin install <source>' to install a plugin.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "Name\tVersion\tSource\tEnabled\tPath")
	fmt.Fprintln(w, "----\t-------\t------\t-------\t----")

	for _, p := range plugins {
		enabled := "✓"
		if !p.Enabled {
			enabled = "✗"
		}
		displayName := p.Name
		// Truncate long paths for display
		displayPath := p.Path
		if home, err := os.UserHomeDir(); err == nil {
			displayPath = strings.Replace(displayPath, home, "~", 1)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", displayName, p.Version, p.SourceType, enabled, displayPath)
	}
	w.Flush()
	fmt.Fprintf(os.Stderr, "\n%d plugin(s) installed. Use 'late plugin enable/disable <name>' to toggle.\n", len(plugins))
}

// handlePluginInstall installs a plugin from the given source.
// Supports --project flag to install into the project-local .late/plugins/ directory.
func handlePluginInstall(pm *PluginManager, args []string) {
	project, source := parseProjectFlag(args)
	if source == "" {
		fmt.Fprintln(os.Stderr, "Error: missing plugin source (npm package name, git URL, or local path)")
		if project && !pm.HasProjectDir() {
			fmt.Fprintln(os.Stderr, "Note: --project flag requires a .late/plugins/ directory (create it first)")
		}
		fmt.Fprintln(os.Stderr, "Usage: late plugin install [--project] <source>")
		return
	}

	var plugin *InstalledPlugin
	var err error

	// Detect source type
	if isGitURL(source) {
		plugin, err = InstallFromGit(pm, source, project)
	} else if isLocalPath(source) {
		plugin, err = InstallFromLocal(pm, source, project)
	} else {
		plugin, err = InstallFromNpm(pm, source, project)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to install plugin: %v\n", err)
		return
	}

	scope := "global"
	if project && pm.HasProjectDir() {
		scope = "project"
	}
	fmt.Printf("Installed plugin: %s v%s (%s)\n", plugin.Name, plugin.Version, scope)
	if plugin.Description != "" {
		fmt.Printf("  %s\n", plugin.Description)
	}
	fmt.Printf("  Path: %s\n", plugin.Path)

	// List available surfaces
	if plugin.Late != nil {
		var surfaces []string
		if len(plugin.Late.Skills) > 0 {
			surfaces = append(surfaces, fmt.Sprintf("%d skill(s)", len(plugin.Late.Skills)))
		}
		if plugin.Late.MCP != nil && len(plugin.Late.MCP.Servers) > 0 {
			surfaces = append(surfaces, fmt.Sprintf("%d MCP server(s)", len(plugin.Late.MCP.Servers)))
		}
		if len(plugin.Late.Commands) > 0 {
			surfaces = append(surfaces, fmt.Sprintf("%d command(s)", len(plugin.Late.Commands)))
		}
		if len(plugin.Late.Themes) > 0 {
			surfaces = append(surfaces, fmt.Sprintf("%d theme(s)", len(plugin.Late.Themes)))
		}
		if plugin.Late.Hooks != nil {
			surfaces = append(surfaces, "hooks")
		}
		if len(surfaces) > 0 {
			fmt.Printf("  Surfaces: %s\n", strings.Join(surfaces, ", "))
		}
	}

	fmt.Println("\nPlugin activated. The filesystem watcher will pick it up within 2 seconds.")
}

// parseProjectFlag checks if --project flag is present in args and returns
// the flag state and remaining args (the source).
func parseProjectFlag(args []string) (project bool, rest string) {
	for i, a := range args {
		if a == "--project" || a == "--local" {
			// Return remaining args after removing the flag
			var remaining []string
			for j, r := range args {
				if j != i {
					remaining = append(remaining, r)
				}
			}
			if len(remaining) > 0 {
				return true, remaining[0]
			}
			return true, ""
		}
	}
	return false, args[0]
}

// handlePluginRemove removes a plugin.
func handlePluginRemove(pm *PluginManager, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: missing plugin name")
		fmt.Fprintln(os.Stderr, "Usage: late plugin remove [--project] <name>")
		return
	}

	project, name := parseProjectFlag(args)
	if name == "" {
		fmt.Fprintln(os.Stderr, "Error: missing plugin name")
		return
	}

	removed, err := RemovePlugin(pm, name, project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to remove plugin: %v\n", err)
		return
	}

	scope := ""
	if project {
		scope = " (project)"
	}
	fmt.Printf("Removed plugin: %s%s\n", name, scope)
}

// handlePluginLink creates a development symlink.
func handlePluginLink(pm *PluginManager, args []string) {
	project, path := parseProjectFlag(args)
	if path == "" {
		fmt.Fprintln(os.Stderr, "Error: missing path")
		fmt.Fprintln(os.Stderr, "Usage: late plugin link [--project] <path>")
		return
	}

	plugin, err := Link(pm, path, project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to link plugin: %v\n", err)
		return
	}

	scope := "global"
	if project {
		scope = "project"
	}
	fmt.Printf("Linked plugin: %s v%s (%s)\n", plugin.Name, plugin.Version, scope)
	fmt.Printf("  Path: %s\n", plugin.Path)
}

// handlePluginUpdate updates installed plugins.
func handlePluginUpdate(pm *PluginManager, args []string) {
	name := ""
	if len(args) > 0 {
		name = args[0]
	}

	if name != "" {
		plugin := pm.Plugin(name)
		if plugin == nil {
			fmt.Fprintf(os.Stderr, "Error: plugin %s is not installed\n", name)
			return
		}
		updatePlugin(pm, plugin)
	} else {
		for _, p := range pm.All() {
			updatePlugin(pm, p)
		}
	}
}

// updatePlugin attempts to update a single plugin based on its source type.
func updatePlugin(pm *PluginManager, plugin *InstalledPlugin) {
	switch plugin.SourceType {
	case "git":
		// For git-installed plugins, pull the latest
		fmt.Printf("Updating %s (git pull)...\n", plugin.Name)
		cmd := exec.Command("git", "-C", plugin.Path, "pull", "--ff-only")
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: git pull failed for %s: %v\n", plugin.Name, err)
		}
	case "npm":
		fmt.Printf("Updating %s (npm update)...\n", plugin.Name)
		cmd := exec.Command("npm", "update", "--prefix", pm.PluginsDir(), plugin.Name)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: npm update failed for %s: %v\n", plugin.Name, err)
		}
	case "local":
		fmt.Printf("Skipping %s (local path — update the source directory directly)\n", plugin.Name)
	default:
		fmt.Printf("Skipping %s (unknown source type: %s)\n", plugin.Name, plugin.SourceType)
	}

	// Reload the plugin manifest
	updated, err := LoadPlugin(plugin.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to reload plugin %s: %v\n", plugin.Name, err)
		return
	}
	updated.SourceType = plugin.SourceType
	updated.Enabled = plugin.Enabled

	if err := SavePluginMeta(updated); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to save metadata: %v\n", err)
	}

	pm.Add(updated)
	fmt.Printf("Updated %s v%s\n", updated.Name, updated.Version)
}

// handlePluginEnable enables or disables a plugin.
func handlePluginEnable(pm *PluginManager, args []string, enable bool) {
	if len(args) == 0 {
		action := "enable"
		if !enable {
			action = "disable"
		}
		fmt.Fprintf(os.Stderr, "Error: missing plugin name\n")
		fmt.Fprintf(os.Stderr, "Usage: late plugin %s <name>\n", action)
		return
	}

	name := args[0]
	plugin := pm.Plugin(name)
	if plugin == nil {
		fmt.Fprintf(os.Stderr, "Error: plugin %s is not installed\n", name)
		return
	}

	plugin.Enabled = enable
	if err := SavePluginMeta(plugin); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save metadata: %v\n", err)
	}

	state := "enabled"
	if !enable {
		state = "disabled"
	}
	fmt.Printf("%s %s\n", plugin.Name, state)
}

// isGitURL checks if a source string looks like a Git URL.
func isGitURL(src string) bool {
	if strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "http://") {
		return strings.HasSuffix(src, ".git") || strings.Contains(src, "github.com/") || strings.Contains(src, "gitlab.com/") || strings.Contains(src, "bitbucket.org/")
	}
	if strings.Contains(src, ":") && !strings.Contains(src, "://") {
		prefix := strings.SplitN(src, ":", 2)[0]
		switch prefix {
		case "github", "gitlab", "bitbucket":
			return true
		}
	}
	return false
}

// isLocalPath checks if a source string looks like a local filesystem path.
func isLocalPath(src string) bool {
	if strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../") || strings.HasPrefix(src, "/") || strings.HasPrefix(src, "~") {
		return true
	}
	// Check if it exists as a local path
	if _, err := os.Stat(src); err == nil {
		return true
	}
	return false
}

// Sort plugins by name
type byName []*InstalledPlugin

func (a byName) Len() int           { return len(a) }
func (a byName) Less(i, j int) bool { return a[i].Name < a[j].Name }
func (a byName) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }

// Ensure sort.Interface compliance
var _ sort.Interface = byName{}
