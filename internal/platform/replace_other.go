//go:build !windows

package platform

import "os"

func replaceFile(src, dst string) error {
	return os.Rename(src, dst)
}
