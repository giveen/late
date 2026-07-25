package plugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// InstallFromNpm installs a plugin from npm by running 'npm install'.
// If project is true and a project dir is configured, installs into the project-local dir.
func InstallFromNpm(pm *PluginManager, pkgName string, projectLocal ...bool) (*InstalledPlugin, error) {
	project := len(projectLocal) > 0 && projectLocal[0]
	targetDir := pm.TargetDir(project)

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create plugins directory: %w", err)
	}

	cmd := exec.Command("npm", "install", "--prefix", targetDir, pkgName)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("npm install failed: %w", err)
	}

	// npm installs into node_modules/<pkgname>
	npmDir := filepath.Join(targetDir, "node_modules", pkgName)
	if _, err := os.Stat(npmDir); os.IsNotExist(err) {
		// Try scoped package path: @scope/name -> node_modules/@scope/name
		parts := strings.SplitN(pkgName, "/", 2)
		if len(parts) == 2 && strings.HasPrefix(pkgName, "@") {
			npmDir = filepath.Join(targetDir, "node_modules", parts[0], parts[1])
		}
		if _, err := os.Stat(npmDir); os.IsNotExist(err) {
			return nil, fmt.Errorf("npm installed but package not found at expected path %s", npmDir)
		}
	}

	// Create parent directories for scoped packages (e.g. @scope/name)
	linkDir := filepath.Join(targetDir, pkgName)
	if err := os.MkdirAll(filepath.Dir(linkDir), 0755); err != nil {
		return nil, fmt.Errorf("failed to create parent directories for symlink: %w", err)
	}

	if _, err := os.Lstat(linkDir); err == nil {
		os.Remove(linkDir)
	}

	// Use relative symlink for portability
	rel, err := filepath.Rel(targetDir, npmDir)
	if err != nil {
		return nil, fmt.Errorf("failed to compute relative path: %w", err)
	}
	if err := os.Symlink(rel, linkDir); err != nil {
		return nil, fmt.Errorf("failed to create symlink: %w", err)
	}

	// Load the plugin from the symlinked directory
	plugin, err := LoadPlugin(linkDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load installed plugin: %w", err)
	}
	plugin.SourceType = "npm"

	if err := SavePluginMeta(plugin); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to save plugin metadata: %v\n", err)
	}

	pm.Add(plugin)
	return plugin, nil
}

// InstallFromGit installs a plugin from a Git repository.
// Supports URLs like https://github.com/user/repo.git and shorthand like github:user/repo.
// If project is true and a project dir is configured, installs into the project-local dir.
func InstallFromGit(pm *PluginManager, url string, projectLocal ...bool) (*InstalledPlugin, error) {
	project := len(projectLocal) > 0 && projectLocal[0]
	destDir := pm.TargetDir(project)

	// Determine plugin name from URL
	name := pluginNameFromURL(url)
	targetDir := filepath.Join(destDir, name)

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create plugins directory: %w", err)
	}

	if _, err := os.Stat(targetDir); err == nil {
		return nil, fmt.Errorf("plugin %s already exists at %s", name, targetDir)
	}

	// Expand shorthand: github:user/repo -> https://github.com/user/repo.git
	gitURL := expandGitURL(url)

	cmd := exec.Command("git", "clone", "--depth", "1", gitURL, targetDir)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Clean up partial clone so the user's next attempt isn't blocked
		// by an "already exists" error against a half-populated directory.
		if rmErr := os.RemoveAll(targetDir); rmErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to remove partial clone dir %s: %v\n", targetDir, rmErr)
		}
		return nil, fmt.Errorf("git clone failed: %w", err)
	}

	// Remove .git to keep the store clean
	os.RemoveAll(filepath.Join(targetDir, ".git"))

	plugin, err := LoadPlugin(targetDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load installed plugin: %w", err)
	}
	plugin.SourceType = "git"

	if err := SavePluginMeta(plugin); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to save plugin metadata: %v\n", err)
	}

	pm.Add(plugin)
	return plugin, nil
}

// InstallFromLocal installs a plugin from a local path by symlinking it
// into the plugins directory. This is equivalent to `late plugin link`.
// If project is true and a project dir is configured, installs into the project-local dir.
func InstallFromLocal(pm *PluginManager, localPath string, projectLocal ...bool) (*InstalledPlugin, error) {
	project := len(projectLocal) > 0 && projectLocal[0]
	destDir := pm.TargetDir(project)

	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("local path does not exist: %s", absPath)
	}

	// Soft sanity check: warn when the requested local plugin lives far
	// outside the user's normal scope (home or current directory). We don't
	// hard-reject because legitimate use cases exist (e.g. /opt/dev-plugins),
	// but the warning helps spot typos and accidental malicious-looking links.
	if isSuspiciousPluginPath(absPath) {
		fmt.Fprintf(os.Stderr, "Warning: linking plugin from path outside $HOME or CWD: %s\n", absPath)
	}

	plugin, err := LoadPlugin(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load plugin from %s: %w", absPath, err)
	}

	// Create a symlink in the plugins directory
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create plugins directory: %w", err)
	}

	targetDir := filepath.Join(destDir, plugin.Name)
	if err := os.MkdirAll(filepath.Dir(targetDir), 0755); err != nil {
		return nil, fmt.Errorf("failed to create parent directories for symlink: %w", err)
	}

	if _, err := os.Lstat(targetDir); err == nil {
		os.Remove(targetDir)
	}

	if err := os.Symlink(absPath, targetDir); err != nil {
		return nil, fmt.Errorf("failed to create symlink: %w", err)
	}

	plugin.Path = targetDir
	plugin.SourceType = "local"

	if err := SavePluginMeta(plugin); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to save plugin metadata: %v\n", err)
	}

	pm.Add(plugin)
	return plugin, nil
}

// RemovePlugin removes a plugin from the global or project-local store and the manager registry.
func RemovePlugin(pm *PluginManager, name string, projectLocal ...bool) (*InstalledPlugin, error) {
	plugin := pm.Plugin(name)
	if plugin == nil {
		return nil, fmt.Errorf("plugin %s is not installed", name)
	}

	project := len(projectLocal) > 0 && projectLocal[0]
	destDir := pm.TargetDir(project)

	if err := removeFromDir(destDir, name); err != nil {
		return plugin, err
	}

	pm.Remove(name)
	return plugin, nil
}

// removeFromDir removes a plugin directory (or symlink) from a specific directory.
func removeFromDir(dir, name string) error {
	targetDir := filepath.Join(dir, name)
	if _, err := os.Lstat(targetDir); err == nil {
		// Check if it's a symlink
		info, err := os.Lstat(targetDir)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			// Just remove the symlink
			if err := os.Remove(targetDir); err != nil {
				return fmt.Errorf("failed to remove symlink: %w", err)
			}
		} else {
			// Remove the whole directory
			if err := os.RemoveAll(targetDir); err != nil {
				return fmt.Errorf("failed to remove plugin directory: %w", err)
			}
		}
	}

	// Also remove from node_modules if it was npm-installed
	npmPath := filepath.Join(dir, "node_modules", name)
	if _, err := os.Stat(npmPath); err == nil {
		os.RemoveAll(npmPath)
	}

	// Remove scoped npm paths
	if strings.HasPrefix(name, "@") {
		parts := strings.SplitN(name, "/", 2)
		if len(parts) == 2 {
			scopedPath := filepath.Join(dir, "node_modules", parts[0], parts[1])
			os.RemoveAll(scopedPath)
			// Remove parent @scope dir if empty
			scopeDir := filepath.Join(dir, "node_modules", parts[0])
			if entries, _ := os.ReadDir(scopeDir); len(entries) == 0 {
				os.Remove(scopeDir)
			}
		}
	}

	return nil
}

// Link creates a development symlink from the plugins directory to a local path.
// If project is true and a project dir is configured, links into the project-local dir.
func Link(pm *PluginManager, localPath string, projectLocal ...bool) (*InstalledPlugin, error) {
	return InstallFromLocal(pm, localPath, projectLocal...)
}

// pluginNameFromURL extracts a plugin name from a Git URL.
func pluginNameFromURL(url string) string {
	// Remove trailing .git
	url = strings.TrimSuffix(url, ".git")

	// Extract the last path component
	parts := strings.Split(url, "/")
	name := parts[len(parts)-1]

	// Handle github:user/repo shorthand
	if strings.Contains(url, ":") && !strings.Contains(url, "://") {
		shorthandParts := strings.Split(url, ":")
		pathParts := strings.Split(shorthandParts[len(shorthandParts)-1], "/")
		if len(pathParts) >= 2 {
			name = pathParts[len(pathParts)-1]
		}
	}

	return name
}

// isSuspiciousPluginPath returns true if the given absolute path is not
// contained within the user's home directory or the process's current
// working directory. Used as a soft warning in `InstallFromLocal` to catch
// obvious typos or suspect link targets — the caller decides how to act.
func isSuspiciousPluginPath(absPath string) bool {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, absPath); err == nil && !strings.HasPrefix(rel, "..") && rel != ".." {
			return false
		}
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		if rel, err := filepath.Rel(cwd, absPath); err == nil && !strings.HasPrefix(rel, "..") && rel != ".." {
			return false
		}
	}
	return true
}

// expandGitURL converts shorthand Git references to proper URLs.
func expandGitURL(url string) string {
	if strings.Contains(url, "://") {
		return url
	}

	// github:user/repo
	if strings.HasPrefix(url, "github:") {
		repo := strings.TrimPrefix(url, "github:")
		return "https://github.com/" + repo + ".git"
	}

	// gitlab:user/repo
	if strings.HasPrefix(url, "gitlab:") {
		repo := strings.TrimPrefix(url, "gitlab:")
		return "https://gitlab.com/" + repo + ".git"
	}

	// bitbucket:user/repo
	if strings.HasPrefix(url, "bitbucket:") {
		repo := strings.TrimPrefix(url, "bitbucket:")
		return "https://bitbucket.org/" + repo + ".git"
	}

	// Assume it's already a valid URL
	return url
}
