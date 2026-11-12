package clients

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"shunt/internal/platform"
)

// BackupPath returns the timestamped backup path for a target file.
func BackupPath(backupDir, client, target string) string {
	name := filepath.Base(target)
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	return filepath.Join(backupDir, client, name+"."+stamp+".bak")
}

// CreateBackup copies the exact bytes of target into the backup directory. A
// missing target is not an error and produces an empty backup path.
func CreateBackup(backupDir, client, target string) (string, error) {
	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", fmt.Errorf("read backup source: %w", err)
	}
	dest := BackupPath(backupDir, client, target)
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", err
	}
	if err := platform.WriteFileAtomic(dest, data, info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("create backup: %w", err)
	}
	return dest, nil
}

// WriteWithBackup creates a backup and atomically replaces the target.
func WriteWithBackup(backupDir, client, target string, data []byte) (string, error) {
	perm := os.FileMode(0o600)
	if info, err := os.Stat(target); err == nil {
		perm = info.Mode().Perm()
	}
	backup, err := CreateBackup(backupDir, client, target)
	if err != nil {
		return "", err
	}
	if err := platform.WriteFileAtomic(target, data, perm); err != nil {
		return backup, err
	}
	return backup, nil
}

// ResolveBackup validates a backup name against the backup directory. Path
// traversal and names outside the directory are rejected.
func ResolveBackup(backupDir, client, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("backup name is empty")
	}
	candidate := name
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(backupDir, client, candidate)
	}
	clean := filepath.Clean(candidate)
	root, err := filepath.Abs(backupDir)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(clean)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("backup path %q is outside the backup directory", name)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("backup not found: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("backup path is a directory")
	}
	return abs, nil
}

// Restore copies a validated backup over target, creating a safety backup of
// the current target first. The backup bytes are restored exactly.
func Restore(backupDir, client, name, target string) (string, error) {
	source, err := ResolveBackup(backupDir, client, name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	safety, err := CreateBackup(backupDir, client, target)
	if err != nil {
		return "", err
	}
	perm := os.FileMode(0o600)
	if info, err := os.Stat(target); err == nil {
		perm = info.Mode().Perm()
	}
	if err := platform.WriteFileAtomic(target, data, perm); err != nil {
		return safety, err
	}
	return safety, nil
}

// EnsureInsideHome refuses to write outside the user's home directory unless
// the destination was given explicitly by the operator.
func EnsureInsideHome(target string, explicit bool) error {
	if explicit {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absHome, absTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("refusing to write outside the home directory: %s", target)
	}
	return nil
}

// DryRun renders the proposed content with the documented header.
func DryRun(target string, data []byte) string {
	return "--- " + target + " ---\n" + string(data)
}
