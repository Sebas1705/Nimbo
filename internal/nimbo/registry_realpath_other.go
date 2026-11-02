//go:build !windows

package nimbo

import "path/filepath"

// registryReal resolves every link in an existing path.
func registryReal(p string) (string, error) { return filepath.EvalSymlinks(p) }
