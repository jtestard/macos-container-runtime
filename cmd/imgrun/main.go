package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"debug/macho"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type descriptor struct {
	MediaType string    `json:"mediaType"`
	Digest    string    `json:"digest"`
	Size      int64     `json:"size"`
	Platform  *platform `json:"platform,omitempty"`
}

type platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
}

type imageIndex struct {
	Manifests []descriptor `json:"manifests"`
}

type imageManifest struct {
	Config descriptor   `json:"config"`
	Layers []descriptor `json:"layers"`
}

type imageConfig struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Config       struct {
		Entrypoint []string `json:"Entrypoint"`
		Cmd        []string `json:"Cmd"`
		WorkingDir string   `json:"WorkingDir"`
		Env        []string `json:"Env"`
		User       string   `json:"User"`
	} `json:"config"`
	RootFS struct {
		Type    string   `json:"type"`
		DiffIDs []string `json:"diff_ids"`
	} `json:"rootfs"`
}

func main() {
	image := flag.String("image", "", "Buildx OCI tarball to run")
	inspect := flag.Bool("inspect", false, "print validated image config as JSON without running")
	flag.Parse()
	if *image == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: imgrun -image <buildx-oci.tar>")
		os.Exit(2)
	}
	if err := run(*image, *inspect); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				os.Exit(128 + int(status.Signal()))
			}
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "imgrun:", err)
		os.Exit(1)
	}
}

func run(image string, inspect bool) error {
	state, err := os.MkdirTemp("", "macnative-run-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(state)
	state, err = filepath.EvalSymlinks(state)
	if err != nil {
		return err
	}
	layout := filepath.Join(state, "layout")
	rootfs := filepath.Join(state, "rootfs")
	if err := unpackLayout(image, layout); err != nil {
		return err
	}
	if err := os.Mkdir(rootfs, 0700); err != nil {
		return err
	}
	var version struct {
		ImageLayoutVersion string `json:"imageLayoutVersion"`
	}
	if err := readJSONFile(filepath.Join(layout, "oci-layout"), &version); err != nil {
		return err
	}
	if version.ImageLayoutVersion != "1.0.0" {
		return fmt.Errorf("unsupported OCI layout version %q", version.ImageLayoutVersion)
	}
	var index imageIndex
	if err := readJSONFile(filepath.Join(layout, "index.json"), &index); err != nil {
		return err
	}
	var selected *descriptor
	for i := range index.Manifests {
		d := &index.Manifests[i]
		if d.Platform != nil && d.Platform.OS == "darwin" && d.Platform.Architecture == "arm64" {
			selected = d
			break
		}
	}
	if selected == nil {
		return errors.New("image has no darwin/arm64 manifest")
	}
	if selected.MediaType != "application/vnd.oci.image.manifest.v1+json" {
		return fmt.Errorf("unsupported manifest media type %q", selected.MediaType)
	}
	var manifest imageManifest
	if err := readJSONBlob(layout, *selected, &manifest); err != nil {
		return err
	}
	if manifest.Config.MediaType != "application/vnd.oci.image.config.v1+json" {
		return fmt.Errorf("unsupported config media type %q", manifest.Config.MediaType)
	}
	var config imageConfig
	if err := readJSONBlob(layout, manifest.Config, &config); err != nil {
		return err
	}
	if config.OS != "darwin" || config.Architecture != "arm64" {
		return fmt.Errorf("image config is %s/%s, expected darwin/arm64", config.OS, config.Architecture)
	}
	if config.RootFS.Type != "layers" || len(config.RootFS.DiffIDs) != len(manifest.Layers) {
		return errors.New("invalid image rootfs layer list")
	}
	if inspect {
		return json.NewEncoder(os.Stdout).Encode(struct {
			imageConfig
			ID string `json:"id"`
		}{imageConfig: config, ID: manifest.Config.Digest})
	}
	for i, layer := range manifest.Layers {
		if err := applyLayer(layout, rootfs, layer, config.RootFS.DiffIDs[i]); err != nil {
			return fmt.Errorf("layer %d: %w", i+1, err)
		}
	}
	return launch(state, rootfs, config)
}

func unpackLayout(image, layout string) error {
	f, err := os.Open(image)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := os.MkdirAll(filepath.Join(layout, "blobs", "sha256"), 0700); err != nil {
		return err
	}
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(h.Name, "/")
		if h.Typeflag == tar.TypeDir {
			if name != "blobs" && name != "blobs/sha256" {
				return fmt.Errorf("unexpected OCI tar directory %q", h.Name)
			}
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return fmt.Errorf("unsupported OCI tar entry %q", h.Name)
		}
		if name != "oci-layout" && name != "index.json" {
			prefix := "blobs/sha256/"
			if !strings.HasPrefix(name, prefix) || !validHexDigest(strings.TrimPrefix(name, prefix)) {
				return fmt.Errorf("unexpected OCI tar file %q", h.Name)
			}
		}
		dest := filepath.Join(layout, filepath.FromSlash(name))
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		written, copyErr := io.Copy(out, tr)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written != h.Size {
			return fmt.Errorf("short OCI tar file %q", h.Name)
		}
	}
}

func validHexDigest(s string) bool {
	if len(s) != 64 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func blobPath(layout string, d descriptor) (string, error) {
	const prefix = "sha256:"
	if !strings.HasPrefix(d.Digest, prefix) || !validHexDigest(strings.TrimPrefix(d.Digest, prefix)) {
		return "", fmt.Errorf("invalid digest %q", d.Digest)
	}
	return filepath.Join(layout, "blobs", "sha256", strings.TrimPrefix(d.Digest, prefix)), nil
}

func verifyBlob(layout string, d descriptor) (string, error) {
	p, err := blobPath(layout, d)
	if err != nil {
		return "", err
	}
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", err
	}
	if n != d.Size || "sha256:"+hex.EncodeToString(h.Sum(nil)) != d.Digest {
		return "", fmt.Errorf("blob %s failed size or digest verification", d.Digest)
	}
	return p, nil
}

func readJSONFile(p string, v any) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() > 16<<20 {
		return fmt.Errorf("metadata file too large: %s", p)
	}
	return json.NewDecoder(f).Decode(v)
}

func readJSONBlob(layout string, d descriptor, v any) error {
	if d.Size < 0 || d.Size > 16<<20 {
		return fmt.Errorf("metadata blob too large: %s", d.Digest)
	}
	p, err := verifyBlob(layout, d)
	if err != nil {
		return err
	}
	return readJSONFile(p, v)
}

func imagePath(root, name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("invalid image path %q", name)
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid image path %q", name)
	}
	return filepath.Join(root, filepath.FromSlash(clean)), nil
}

func applyLayer(layout, root string, d descriptor, expectedDiffID string) error {
	if d.MediaType != "application/vnd.oci.image.layer.v1.tar+gzip" {
		return fmt.Errorf("unsupported layer media type %q", d.MediaType)
	}
	if !strings.HasPrefix(expectedDiffID, "sha256:") || !validHexDigest(strings.TrimPrefix(expectedDiffID, "sha256:")) {
		return fmt.Errorf("invalid diff ID %q", expectedDiffID)
	}
	p, err := verifyBlob(layout, d)
	if err != nil {
		return err
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	hash := sha256.New()
	content := io.TeeReader(gz, hash)
	tr := tar.NewReader(content)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		dest, err := imagePath(root, h.Name)
		if err != nil {
			return err
		}
		if strings.HasPrefix(path.Base(h.Name), ".wh.") {
			return fmt.Errorf("whiteouts are not supported: %q", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			written, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if written != h.Size {
				return fmt.Errorf("short layer file %q", h.Name)
			}
			if err := os.Chmod(dest, os.FileMode(h.Mode)&0777); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported layer entry %q (type %d)", h.Name, h.Typeflag)
		}
	}
	if _, err := io.Copy(io.Discard, content); err != nil {
		return err
	}
	if "sha256:"+hex.EncodeToString(hash.Sum(nil)) != expectedDiffID {
		return errors.New("uncompressed layer digest mismatch")
	}
	return nil
}

func launch(state, root string, cfg imageConfig) error {
	if cfg.Config.User != "" && cfg.Config.User != "root" {
		return fmt.Errorf("image user %q is not supported", cfg.Config.User)
	}
	if len(cfg.Config.Entrypoint) == 0 {
		return errors.New("image has no entrypoint")
	}
	workdir := root
	if cfg.Config.WorkingDir != "" && cfg.Config.WorkingDir != "/" {
		if !strings.HasPrefix(cfg.Config.WorkingDir, "/") {
			return fmt.Errorf("relative working directory %q is not supported", cfg.Config.WorkingDir)
		}
		var err error
		workdir, err = imagePath(root, strings.TrimPrefix(cfg.Config.WorkingDir, "/"))
		if err != nil {
			return err
		}
	}
	info, err := os.Stat(workdir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("working directory is missing: %s", cfg.Config.WorkingDir)
	}
	entry := cfg.Config.Entrypoint[0]
	if filepath.IsAbs(entry) || !strings.Contains(entry, "/") {
		return fmt.Errorf("entrypoint must be a relative path: %q", entry)
	}
	executable := filepath.Join(workdir, filepath.Clean(entry))
	rel, err := filepath.Rel(root, executable)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("entrypoint escapes image: %q", entry)
	}
	mf, err := macho.Open(executable)
	if err != nil {
		return fmt.Errorf("entrypoint is not Mach-O: %w", err)
	}
	if mf.Cpu != macho.CpuArm64 {
		mf.Close()
		return fmt.Errorf("entrypoint CPU is %s, expected arm64", mf.Cpu)
	}
	mf.Close()
	home := filepath.Join(state, "home")
	tmp := filepath.Join(state, "tmp")
	if err := os.MkdirAll(home, 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(tmp, 0700); err != nil {
		return err
	}
	env := []string{"PATH=/usr/bin:/bin"}
	for _, entry := range cfg.Config.Env {
		if !strings.Contains(entry, "=") {
			return fmt.Errorf("invalid image environment entry %q", entry)
		}
		env = append(env, entry)
	}
	env = append(env, "HOME="+home, "TMPDIR="+tmp)
	profile := fmt.Sprintf("(version 1)(allow default)(deny file-write*)(allow file-write* (subpath %s))", strconv.Quote(state))
	args := append([]string{"-p", profile, executable}, cfg.Config.Entrypoint[1:]...)
	args = append(args, cfg.Config.Cmd...)
	cmd := exec.Command("/usr/bin/sandbox-exec", args...)
	cmd.Dir = workdir
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "imgrun: started pid %d from %s\n", cmd.Process.Pid, workdir)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGUSR1)
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-signals:
			if unixSig, ok := sig.(syscall.Signal); ok {
				// The daemon uses SIGUSR1 for Docker's force-removal path.
				// Kill the sandboxed process group but let this runner
				// reap it and remove the temporary filesystem.
				if unixSig == syscall.SIGUSR1 {
					unixSig = syscall.SIGKILL
				}
				_ = syscall.Kill(-cmd.Process.Pid, unixSig)
			}
		case <-done:
		}
	}()
	err = cmd.Wait()
	close(done)
	signal.Stop(signals)
	return err
}
