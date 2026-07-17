// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
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

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/runtime/incus"
	"github.com/openbox-dev/openbox/internal/version"
	_ "modernc.org/sqlite"
)

const backupFormatVersion = 2

type backupManifest struct {
	FormatVersion int              `json:"format_version"`
	CreatedAt     time.Time        `json:"created_at"`
	OpenBox       string           `json:"openbox_version"`
	Files         []backupFile     `json:"files"`
	Instances     []backupInstance `json:"instances,omitempty"`
}

type backupFile struct {
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// backupInstance ties an Incus-native export to its durable control-plane row.
// The file digest is recorded in Files; this record makes restore validation
// independent from a runtime's archive format.
type backupInstance struct {
	ID         string `json:"id"`
	RuntimeRef string `json:"runtime_ref"`
	Path       string `json:"path"`
}

type backupRuntime interface {
	InspectInstance(context.Context, string) (runtimeapi.Instance, error)
	ExportInstance(context.Context, string, io.Writer) error
	ImportInstance(context.Context, runtimeapi.InstanceBackup) (runtimeapi.Instance, error)
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
	case "restore":
		return runBackupRestore(args[1:], jsonOutput, stdout, stderr)
	default:
		return backupUsage(stderr)
	}
}

func backupUsage(stderr io.Writer) int {
	fmt.Fprintf(stderr, "%s", `usage: openbox backup <create|verify|restore> ...

Examples:
  sudo openbox backup create /srv/backups/openbox-$(date -u +%Y%m%dT%H%M%SZ)
  sudo openbox backup verify /srv/backups/openbox-20260717T083000Z
  sudo openbox backup restore /srv/backups/openbox-20260717T083000Z
`)
	return 2
}

func runBackupCreate(args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("openbox backup create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	databasePath := flags.String("database", "/var/lib/openbox/openbox.db", "SQLite database path")
	stateDir := flags.String("state-dir", "/var/lib/openbox", "OpenBox state directory")
	configPath := flags.String("config", "/etc/openbox/openboxd.env", "openboxd environment file (optional)")
	includeInstances := flags.Bool("instances", false, "include stopped Incus instance disks")
	incusSocket := flags.String("incus-socket", incus.DefaultSocket, "Incus Unix socket")
	incusProject := flags.String("incus-project", "openbox", "Incus project")
	positionals, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 1 {
		return usageError(stderr, "usage: openbox backup create DIRECTORY [--database PATH] [--state-dir PATH] [--config PATH] [--instances]")
	}
	var runtime backupRuntime
	if *includeInstances {
		runtime, err = newBackupRuntime(*incusSocket, *incusProject)
		if err != nil {
			return commandError(stderr, err)
		}
	}
	manifest, err := createBackupWithRuntime(positionals[0], *databasePath, *stateDir, *configPath, runtime)
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, manifest)
	}
	fmt.Fprintf(stdout, "backup: %s\nfiles: %d\ninstances: %d\ncreated_at: %s\n", positionals[0], len(manifest.Files), len(manifest.Instances), manifest.CreatedAt.Format(time.RFC3339))
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

func runBackupRestore(args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("openbox backup restore", flag.ContinueOnError)
	flags.SetOutput(stderr)
	databasePath := flags.String("database", "/var/lib/openbox/openbox.db", "SQLite database path")
	stateDir := flags.String("state-dir", "/var/lib/openbox", "OpenBox state directory")
	configPath := flags.String("config", "/etc/openbox/openboxd.env", "openboxd environment file")
	incusSocket := flags.String("incus-socket", incus.DefaultSocket, "Incus Unix socket")
	incusProject := flags.String("incus-project", "openbox", "Incus project")
	positionals, err := parseInterspersed(flags, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 1 {
		return usageError(stderr, "usage: openbox backup restore DIRECTORY [--database PATH] [--state-dir PATH] [--config PATH]")
	}
	manifest, err := verifyBackup(positionals[0])
	if err != nil {
		return commandError(stderr, err)
	}
	var runtime backupRuntime
	if len(manifest.Instances) > 0 {
		runtime, err = newBackupRuntime(*incusSocket, *incusProject)
		if err != nil {
			return commandError(stderr, err)
		}
	}
	manifest, err = restoreBackup(positionals[0], *databasePath, *stateDir, *configPath, runtime)
	if err != nil {
		return commandError(stderr, err)
	}
	if jsonOutput {
		return encodeJSON(stdout, stderr, manifest)
	}
	fmt.Fprintf(stdout, "backup restored: %s\nfiles: %d\ninstances: %d\n", positionals[0], len(manifest.Files), len(manifest.Instances))
	return 0
}

func createBackup(destination, databasePath, stateDir, configPath string) (manifest backupManifest, err error) {
	return createBackupWithRuntime(destination, databasePath, stateDir, configPath, nil)
}

func createBackupWithRuntime(destination, databasePath, stateDir, configPath string, runtime backupRuntime) (manifest backupManifest, err error) {
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
	var instances []backupInstance
	if runtime != nil {
		instances, err = exportInstanceBackups(databaseDestination, destination, runtime)
		if err != nil {
			return manifest, err
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
		Instances:     instances,
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

func newBackupRuntime(socketPath, project string) (*incus.Adapter, error) {
	runtime, err := incus.New(incus.Options{SocketPath: socketPath, Project: project})
	if err != nil {
		return nil, fmt.Errorf("configure Incus backup runtime: %w", err)
	}
	return runtime, nil
}

func exportInstanceBackups(databasePath, destination string, runtime backupRuntime) ([]backupInstance, error) {
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open backed up SQLite database for instance exports: %w", err)
	}
	defer database.Close()
	rows, err := database.Query(`SELECT id, runtime_ref FROM instances WHERE deleted_at IS NULL AND runtime_ref != '' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list backed up instances: %w", err)
	}
	defer rows.Close()
	var exports []backupInstance
	for rows.Next() {
		var item backupInstance
		if err := rows.Scan(&item.ID, &item.RuntimeRef); err != nil {
			return nil, fmt.Errorf("read backed up instance: %w", err)
		}
		instance, err := runtime.InspectInstance(context.Background(), item.RuntimeRef)
		if err != nil {
			return nil, fmt.Errorf("inspect instance %s before export: %w", item.RuntimeRef, err)
		}
		if instance.State != runtimeapi.StateStopped {
			return nil, fmt.Errorf("instance %s is %s; stop instances before creating an --instances backup", item.RuntimeRef, instance.State)
		}
		if strings.ContainsAny(item.ID, `/\`) {
			return nil, fmt.Errorf("invalid instance ID for backup export: %q", item.ID)
		}
		item.Path = filepath.ToSlash(filepath.Join("instances", item.ID+".tar"))
		path, err := backupPath(destination, item.Path)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create instance backup directory: %w", err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, fmt.Errorf("create instance export %s: %w", item.RuntimeRef, err)
		}
		exportErr := runtime.ExportInstance(context.Background(), item.RuntimeRef, file)
		closeErr := file.Close()
		if exportErr != nil {
			return nil, fmt.Errorf("export instance %s: %w", item.RuntimeRef, exportErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close instance export %s: %w", item.RuntimeRef, closeErr)
		}
		exports = append(exports, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list backed up instances: %w", err)
	}
	return exports, nil
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
	if manifest.FormatVersion != 1 && manifest.FormatVersion != backupFormatVersion {
		return backupManifest{}, fmt.Errorf("unsupported backup format %d", manifest.FormatVersion)
	}
	hasDatabase := false
	filesByPath := make(map[string]backupFile, len(manifest.Files))
	for _, expected := range manifest.Files {
		if _, exists := filesByPath[expected.Path]; exists {
			return backupManifest{}, fmt.Errorf("backup manifest contains duplicate file %q", expected.Path)
		}
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
		filesByPath[expected.Path] = expected
		hasDatabase = hasDatabase || expected.Path == "data/openbox.db"
	}
	if !hasDatabase || !hasSSHFiles(manifest.Files) {
		return backupManifest{}, errors.New("backup is missing required database or SSH gateway state")
	}
	if err := verifySQLite(filepath.Join(root, "data", "openbox.db")); err != nil {
		return backupManifest{}, err
	}
	instanceIDs := map[string]bool{}
	instanceRefs := map[string]bool{}
	for _, instance := range manifest.Instances {
		if instance.ID == "" || instance.RuntimeRef == "" || instance.Path == "" {
			return backupManifest{}, errors.New("backup manifest contains incomplete instance export")
		}
		if !strings.HasPrefix(instance.Path, "instances/") {
			return backupManifest{}, fmt.Errorf("invalid instance export path %q", instance.Path)
		}
		if instanceIDs[instance.ID] || instanceRefs[instance.RuntimeRef] {
			return backupManifest{}, errors.New("backup manifest contains duplicate instance export")
		}
		if _, err := backupPath(root, instance.Path); err != nil {
			return backupManifest{}, err
		}
		if _, exists := filesByPath[instance.Path]; !exists {
			return backupManifest{}, fmt.Errorf("instance export is missing from backup files: %s", instance.Path)
		}
		instanceIDs[instance.ID] = true
		instanceRefs[instance.RuntimeRef] = true
	}
	return manifest, nil
}

// restoreBackup verifies source before changing host state. Call it only while
// openboxd is stopped: it replaces the database, SSH gateway state, generated
// Caddy routes, and (when present) daemon environment file.
func restoreBackup(source, databasePath, stateDir, configPath string, runtime backupRuntime) (backupManifest, error) {
	manifest, err := verifyBackup(source)
	if err != nil {
		return backupManifest{}, err
	}
	if len(manifest.Instances) > 0 && runtime == nil {
		return backupManifest{}, errors.New("backup includes instance exports; an Incus runtime is required to restore them")
	}
	if err := importInstanceBackups(source, manifest, runtime); err != nil {
		return backupManifest{}, err
	}
	filesByPath := make(map[string]backupFile, len(manifest.Files))
	for _, file := range manifest.Files {
		filesByPath[file.Path] = file
	}
	databaseFile := filesByPath["data/openbox.db"]
	if err := restoreRegularBackupFile(source, databaseFile, databasePath, 0o600); err != nil {
		return backupManifest{}, fmt.Errorf("restore SQLite database: %w", err)
	}
	_ = os.Remove(databasePath + "-wal")
	_ = os.Remove(databasePath + "-shm")
	if err := restoreSSHBackupTree(source, manifest.Files, filepath.Join(stateDir, "ssh")); err != nil {
		return backupManifest{}, fmt.Errorf("restore SSH gateway state: %w", err)
	}
	if routeFile, ok := filesByPath["caddy/routes.caddyfile"]; ok {
		if err := restoreRegularBackupFile(source, routeFile, filepath.Join(stateDir, "caddy", "routes.caddyfile"), 0o644); err != nil {
			return backupManifest{}, fmt.Errorf("restore generated Caddy routes: %w", err)
		}
	}
	if configFile, ok := filesByPath["config/openboxd.env"]; ok && configPath != "" {
		if err := restoreRegularBackupFile(source, configFile, configPath, 0o600); err != nil {
			return backupManifest{}, fmt.Errorf("restore openboxd environment: %w", err)
		}
	}
	return manifest, nil
}

func importInstanceBackups(root string, manifest backupManifest, runtime backupRuntime) error {
	for _, item := range manifest.Instances {
		path, err := backupPath(root, item.Path)
		if err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open instance export %s: %w", item.RuntimeRef, err)
		}
		instance, importErr := runtime.ImportInstance(context.Background(), runtimeapi.InstanceBackup{Ref: item.RuntimeRef, Body: file})
		closeErr := file.Close()
		if importErr != nil {
			return fmt.Errorf("import instance %s: %w", item.RuntimeRef, importErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close instance export %s: %w", item.RuntimeRef, closeErr)
		}
		if instance.Ref != item.RuntimeRef || instance.Metadata["user.openbox.instance_id"] != item.ID {
			return fmt.Errorf("imported instance %s does not retain OpenBox ownership metadata", item.RuntimeRef)
		}
	}
	return nil
}

func restoreSSHBackupTree(root string, files []backupFile, destination string) error {
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".openbox-restore-ssh-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	for _, file := range files {
		if !strings.HasPrefix(file.Path, "ssh/") {
			continue
		}
		relative := strings.TrimPrefix(file.Path, "ssh/")
		if relative == "" {
			return errors.New("invalid SSH backup path")
		}
		target, err := backupPath(staging, relative)
		if err != nil {
			return err
		}
		if err := restoreRegularBackupFile(root, file, target, 0o600); err != nil {
			return err
		}
	}
	if err := os.Chmod(staging, 0o700); err != nil {
		return err
	}
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	return os.Rename(staging, destination)
}

func restoreRegularBackupFile(root string, file backupFile, destination string, mode fs.FileMode) error {
	if file.Path == "" {
		return errors.New("backup file is required")
	}
	source, err := backupPath(root, file.Path)
	if err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	output, err := os.CreateTemp(filepath.Dir(destination), ".openbox-restore-")
	if err != nil {
		return err
	}
	staged := output.Name()
	defer os.Remove(staged)
	if err := output.Chmod(mode); err != nil {
		_ = output.Close()
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	return os.Rename(staged, destination)
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
