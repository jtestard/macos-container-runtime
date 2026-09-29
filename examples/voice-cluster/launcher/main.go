// launcher starts a packaged macOS program from the extracted OCI image root.
// Paths in launch.json are relative to /app, which imgrun places at a fresh
// location for each run. ${ROOT} in environment values expands to that path.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type config struct {
	Program string            `json:"program"`
	Cwd     string            `json:"cwd"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "voice launcher:", err)
		os.Exit(1)
	}
}

func run() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(executable), ".."))
	data, err := os.ReadFile(filepath.Join(root, "launch.json"))
	if err != nil {
		return err
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	if cfg.Program == "" || filepath.IsAbs(cfg.Program) {
		return fmt.Errorf("program must be relative to image app directory")
	}
	program := filepath.Clean(filepath.Join(root, cfg.Program))
	rel, err := filepath.Rel(root, program)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("program escapes image app directory")
	}
	cwd := root
	if cfg.Cwd != "" {
		if filepath.IsAbs(cfg.Cwd) {
			return fmt.Errorf("working directory must be relative to image app directory")
		}
		cwd = filepath.Clean(filepath.Join(root, cfg.Cwd))
		cwdRel, err := filepath.Rel(root, cwd)
		if err != nil || cwdRel == ".." || strings.HasPrefix(cwdRel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("working directory escapes image app directory")
		}
	}
	if err := os.Chdir(cwd); err != nil {
		return err
	}
	for key, value := range cfg.Env {
		if key == "" || strings.ContainsAny(key, "=\x00") {
			return fmt.Errorf("invalid environment name %q", key)
		}
		expanded := strings.ReplaceAll(value, "${ROOT}", root)
		if key == "HOME" || key == "TMPDIR" {
			rel, err := filepath.Rel(root, expanded)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("%s escapes image app directory", key)
			}
			if err := os.MkdirAll(expanded, 0700); err != nil {
				return err
			}
		}
		if err := os.Setenv(key, expanded); err != nil {
			return err
		}
	}
	argv := append([]string{program}, cfg.Args...)
	return syscall.Exec(program, argv, os.Environ())
}
