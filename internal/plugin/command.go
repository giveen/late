package plugin

import (
	"fmt"
	"os"
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

	// Single dispatcher: classifies URL/path/npm/layout and falls through
	// to marketplace → npm for unresolved bare names.
	plugin, err := Install(pm, source, nil, project)
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
//
// Behavior:
//   - `late plugin update` (no args)    → UpdateAll — re-installs every npm/git
//     plugin in place, skipping local.
//   - `late plugin update <name>`       → Update one plugin by name. Resolves
//     marketplace-source plugins via the default marketplace client on the fly.
//   - `late plugin update <name> local` → Refused with a hint to edit the
//     source directory directly.
func handlePluginUpdate(pm *PluginManager, args []string) {
	if len(args) > 0 {
		name := args[0]
		if _, err := Update(pm, name, nil); err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to update plugin %s: %v\n", name, err)
			return
		}
		return
	}

	results, err := UpdateAll(pm, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: bulk update failed: %v\n", err)
		// Surface partial state so the user can see what succeeded.
		for _, p := range results {
			fmt.Printf("  %s v%s\n", p.Name, p.Version)
		}
		return
	}
	for _, p := range results {
		fmt.Printf("Updated %s v%s\n", p.Name, p.Version)
	}
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

// (isGitURL and isLocalPath were removed: Install() now classifies the
// source string itself, including marketplace fallback for bare names.)

// Sort plugins by name
type byName []*InstalledPlugin

func (a byName) Len() int           { return len(a) }
func (a byName) Less(i, j int) bool { return a[i].Name < a[j].Name }
func (a byName) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }

// Ensure sort.Interface compliance
var _ sort.Interface = byName{}
