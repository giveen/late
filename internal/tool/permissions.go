package tool

import (
	"os"
	"path/filepath"
	"strings"
)

// IsSafePath checks if a path is within the current working directory.
func IsSafePath(path string) bool {
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	// Normalize cwd to have no trailing slash for consistent comparison
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return false
	}
	// Ensure absCwd ends with path separator for proper prefix matching
	if !strings.HasSuffix(absCwd, string(filepath.Separator)) {
		absCwd += string(filepath.Separator)
	}

	// Handle root path case
	if absCwd == string(filepath.Separator) {
		return true
	}

	return strings.HasPrefix(absPath, absCwd)
}
