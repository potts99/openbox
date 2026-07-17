// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openbox-dev/openbox/internal/version"
	_ "modernc.org/sqlite"
)

const backupFormatVersion = 1

type backupManifest struct {
	FormatVersion int          `json:"format_version"`
	CreatedAt     time.Time    `json:"created_at"`
	OpenBox       string       `json:"openbox_version"`
	Files         []backupFile `json:"files"`
}

type backupFile struct {
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func runBackup(args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return backupUsage(stderr)
	}
	switch args[0] {
	case "create":
		return runBackupCreate(args[1:], jsonOutput, stdout, stderr)
	case "verify":
		return runBackupVerify(args[1:], jsonOutput, stdout, stderr)
	default:
		return backupUsage(stderr)
	}
}

func backupUsage(stderr io.Writer) int {
	fmt.Fprintf(stderr, "%s", `usage: openbox backup <create|verify> ...

Examples:
  sudo openbox backup create /srv/backups/openbox-$(date -u +%Y%m%dT%H%M%SZ)
  sudo openbox backup verify /srv/backups/openbox-20260717T083000Z
`)
	return 2
}

func runBackupCreate(args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("openbox backup create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	databasePath := flags.String("database", "/var/lib/openbox/openbox.db", "SQLite database path")
	stateDir := flags.String("state-dir", "/var/lib/openbox", "OpenBox state directory")
	configPath := flags.String("config", "/etc/openbox/openboxd.env", "openboxd environment file (optional)")
	positionals, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 1 {
		return usageError(stderr, "usage: openbox backup create DIRECTORY [--database PATH] [--state-dir PATH] [--config PATH]")
	}
	manifest, err := createBackup(positionals[0], *databasePath, *stateDir, *configPath)
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, manifest)
	}
	fmt.Fprintf(stdout, "backup: %s\nfiles: %d\ncreated_at: %s\n", positionals[0], len(manifest.Files), manifest.CreatedAt.Format(time.RFC3339))
	return 0
}

func runBackupVerify(args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return usageError(stderr, "usage: openbox backup verify DIRECTORY")
	}
	manifest, err := verifyBackup(args[0])
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, manifest)
	}
	fmt.Fprintf(stdout, "backup verified: %s\nfiles: %d\ncreated_at: %s\n", args[0], len(manifest.Files), manifest.CreatedAt.Format(time.RFC3339))
	return 0
}

func createBackup(destination, databasePath, stateDir, configPath string) (manifest backupManifest, err error) {
	if destination == "" {
		return manifest, errors.New("backup destination is required")
	}
	if _, err := os.Stat(destination); err == nil {
		return manifest, fmt.Errorf("backup destination already exists: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return manifest, fmt.Errorf("check backup destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return manifest, fmt.Errorf("create backup parent: %w", err)
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return manifest, fmt.Errorf("create backup destination: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(destination)
		}
	}()

	databaseDestination := filepath.Join(destination, "data", "openbox.db")
	if err = os.MkdirAll(filepath.Dir(databaseDestination), 0o700); err != nil {
		return manifest, fmt.Errorf("create database backup directory: %w", err)
	}
	if err = backupSQLite(databasePath, databaseDestination); err != nil {
		return manifest, err
	}
	if err = copyTree(filepath.Join(stateDir, "ssh"), filepath.Join(destination, "ssh")); err != nil {
		return manifest, fmt.Errorf("copy SSH gateway state: %w", err)
	}
	if _, err = copyOptionalRegularFile(filepath.Join(stateDir, "caddy", "routes.caddyfile"), filepath.Join(destination, "caddy", "routes.caddyfile")); err != nil {
		return manifest, fmt.Errorf("copy generated Caddy routes: %w", err)
	}
	if configPath != "" {
		if _, err = copyOptionalRegularFile(configPath, filepath.Join(destination, "config", "openboxd.env")); err != nil {
			return manifest, fmt.Errorf("copy openboxd environment: %w", err)
		}
	}
	files, err := listBackupFiles(destination)
	if err != nil {
		return manifest, err
	}
	if !hasSSHFiles(files) {
		return manifest, errors.New("SSH gateway state is empty")
	}
	manifest = backupManifest{
		FormatVersion: backupFormatVersion,
		CreatedAt:     time.Now().UTC(),
		OpenBox:       version.Version,
		Files:         files,
	}
	if err := writeBackupManifest(destination, manifest); err != nil {
		return backupManifest{}, err
	}
	return manifest, nil
}

func backupSQLite(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("stat SQLite database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("SQLite database is not a regular file: %s", source)
	}
	database, err := sql.Open("sqlite", source)
	if err != nil {
		return fmt.Errorf("open SQLite database: %w", err)
	}
	defer database.Close()
	quotedDestination := "'" + strings.ReplaceAll(destination, "'", "''") + "'"
	if _, err := database.Exec("VACUUM INTO " + quotedDestination); err != nil {
		return fmt.Errorf("create consistent SQLite backup: %w", err)
	}
	return nil
}

func copyTree(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", source)
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink %s", path)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("refusing non-regular file %s", path)
		}
		return copyRegularFile(path, target)
	})
}

func copyRegularFile(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", source)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

func copyOptionalRegularFile(source, destination string) (bool, error) {
	if _, err := os.Stat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if err := copyRegularFile(source, destination); err != nil {
		return false, err
	}
	return true, nil
}

func listBackupFiles(root string) ([]backupFile, error) {
	var files []backupFile
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("refusing non-regular backup file %s", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sum, size, err := hashFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, backupFile{Path: filepath.ToSlash(relative), Mode: uint32(info.Mode().Perm()), Size: size, SHA256: sum})
		return nil
	})
	return files, err
}

func writeBackupManifest(root string, manifest backupManifest) error {
	file, err := os.OpenFile(filepath.Join(root, "manifest.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create backup manifest: %w", err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(manifest); err != nil {
		return fmt.Errorf("write backup manifest: %w", err)
	}
	return nil
}

func verifyBackup(root string) (backupManifest, error) {
	file, err := os.Open(filepath.Join(root, "manifest.json"))
	if err != nil {
		return backupManifest{}, fmt.Errorf("read backup manifest: %w", err)
	}
	defer file.Close()
	var manifest backupManifest
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return backupManifest{}, fmt.Errorf("decode backup manifest: %w", err)
	}
	if manifest.FormatVersion != backupFormatVersion {
		return backupManifest{}, fmt.Errorf("unsupported backup format %d", manifest.FormatVersion)
	}
	hasDatabase := false
	for _, expected := range manifest.Files {
		path, err := backupPath(root, expected.Path)
		if err != nil {
			return backupManifest{}, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return backupManifest{}, fmt.Errorf("read backup file %s: %w", expected.Path, err)
		}
		if !info.Mode().IsRegular() {
			return backupManifest{}, fmt.Errorf("backup file %s is not regular", expected.Path)
		}
		sum, size, err := hashFile(path)
		if err != nil {
			return backupManifest{}, fmt.Errorf("hash backup file %s: %w", expected.Path, err)
		}
		if size != expected.Size || sum != expected.SHA256 {
			return backupManifest{}, fmt.Errorf("backup file integrity check failed: %s", expected.Path)
		}
		hasDatabase = hasDatabase || expected.Path == "data/openbox.db"
	}
	if !hasDatabase || !hasSSHFiles(manifest.Files) {
		return backupManifest{}, errors.New("backup is missing required database or SSH gateway state")
	}
	if err := verifySQLite(filepath.Join(root, "data", "openbox.db")); err != nil {
		return backupManifest{}, err
	}
	return manifest, nil
}

func hasSSHFiles(files []backupFile) bool {
	for _, file := range files {
		if strings.HasPrefix(file.Path, "ssh/") {
			return true
		}
	}
	return false
}

func backupPath(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", fmt.Errorf("invalid backup path %q", relative)
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	resolved, err := filepath.Rel(root, path)
	if err != nil || resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid backup path %q", relative)
	}
	return path, nil
}

func verifySQLite(path string) error {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open backed up SQLite database: %w", err)
	}
	defer database.Close()
	var integrity string
	if err := database.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		return fmt.Errorf("check backed up SQLite database: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("backed up SQLite database integrity check: %s", integrity)
	}
	return nil
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}
