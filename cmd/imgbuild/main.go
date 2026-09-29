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
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	configType   = "application/vnd.oci.image.config.v1+json"
	manifestType = "application/vnd.oci.image.manifest.v1+json"
	indexType    = "application/vnd.oci.image.index.v1+json"
	layerType    = "application/vnd.oci.image.layer.v1.tar+gzip"
)

type descriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Platform    *platform         `json:"platform,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
}

type imageConfig struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Config       struct {
		Entrypoint []string `json:"Entrypoint"`
		Cmd        []string `json:"Cmd"`
		WorkingDir string   `json:"WorkingDir"`
	} `json:"config"`
	RootFS struct {
		Type    string   `json:"type"`
		DiffIDs []string `json:"diff_ids"`
	} `json:"rootfs"`
}

type imageManifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        descriptor   `json:"config"`
	Layers        []descriptor `json:"layers"`
}

type imageIndex struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Manifests     []descriptor `json:"manifests"`
}

type countedWriter struct {
	w io.Writer
	n int64
}

func (c *countedWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func main() {
	source := flag.String("source", "", "go-inf-server project directory")
	output := flag.String("output", "", "new OCI image layout directory")
	tag := flag.String("tag", "go-inf-server:smollm2", "local OCI reference name")
	flag.Parse()
	if *source == "" || *output == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: imgbuild -source <go-inf-server> -output <oci-layout> [-tag <name>]")
		os.Exit(2)
	}
	if err := build(*source, *output, *tag); err != nil {
		fmt.Fprintln(os.Stderr, "imgbuild:", err)
		os.Exit(1)
	}
}

func build(source, output, tag string) error {
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	if _, err := os.Stat(output); err == nil {
		return fmt.Errorf("output already exists: %s", output)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := checkMachO(filepath.Join(source, "bin/server")); err != nil {
		return err
	}
	libs, err := filepath.Glob(filepath.Join(source, "lib", "*.dylib"))
	if err != nil {
		return err
	}
	if len(libs) == 0 {
		return fmt.Errorf("no dylibs under %s/lib", source)
	}
	for _, lib := range libs {
		if err := checkMachO(lib); err != nil {
			return err
		}
	}
	appFiles := []string{"bin/server", "config.smollm2.toml"}
	for _, lib := range libs {
		appFiles = append(appFiles, "lib/"+filepath.Base(lib))
	}
	modelFiles := []string{"models/smollm2-360m/SmolLM2-360M-Instruct-Q8_0.gguf"}

	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(output), ".imgbuild-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.MkdirAll(filepath.Join(stage, "blobs", "sha256"), 0755); err != nil {
		return err
	}

	appLayer, appDiffID, err := writeLayer(stage, source, appFiles)
	if err != nil {
		return fmt.Errorf("application layer: %w", err)
	}
	modelLayer, modelDiffID, err := writeLayer(stage, source, modelFiles)
	if err != nil {
		return fmt.Errorf("model layer: %w", err)
	}
	var cfg imageConfig
	cfg.Architecture, cfg.OS = "arm64", "darwin"
	cfg.Config.Entrypoint = []string{"bin/server"}
	cfg.Config.Cmd = []string{"-config", "config.smollm2.toml"}
	cfg.Config.WorkingDir = "/"
	cfg.RootFS.Type = "layers"
	cfg.RootFS.DiffIDs = []string{appDiffID, modelDiffID}
	cfgDesc, err := writeJSONBlob(stage, configType, cfg)
	if err != nil {
		return err
	}
	manifestDesc, err := writeJSONBlob(stage, manifestType, imageManifest{
		SchemaVersion: 2,
		MediaType:     manifestType,
		Config:        cfgDesc,
		Layers:        []descriptor{appLayer, modelLayer},
	})
	if err != nil {
		return err
	}
	manifestDesc.Platform = &platform{Architecture: "arm64", OS: "darwin"}
	manifestDesc.Annotations = map[string]string{"org.opencontainers.image.ref.name": tag}
	idx := imageIndex{SchemaVersion: 2, MediaType: indexType, Manifests: []descriptor{manifestDesc}}
	if err := writeJSONFile(filepath.Join(stage, "index.json"), idx); err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(stage, "oci-layout"), struct {
		Version string `json:"imageLayoutVersion"`
	}{Version: "1.0.0"}); err != nil {
		return err
	}
	if err := os.Rename(stage, output); err != nil {
		return err
	}
	fmt.Printf("image %s (%s)\n", tag, manifestDesc.Digest)
	fmt.Printf("app layer %s (%d bytes)\n", appLayer.Digest, appLayer.Size)
	fmt.Printf("model layer %s (%d bytes)\n", modelLayer.Digest, modelLayer.Size)
	fmt.Printf("OCI layout: %s\n", output)
	return nil
}

func checkMachO(path string) error {
	f, err := macho.Open(path)
	if err != nil {
		return fmt.Errorf("read Mach-O %s: %w", path, err)
	}
	defer f.Close()
	if f.Cpu != macho.CpuArm64 {
		return fmt.Errorf("%s is %s, expected arm64", path, f.Cpu)
	}
	return nil
}

func writeLayer(layoutDir, source string, paths []string) (descriptor, string, error) {
	var empty descriptor
	tmp, err := os.CreateTemp(filepath.Join(layoutDir, "blobs", "sha256"), ".layer-")
	if err != nil {
		return empty, "", err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	compressedHash := sha256.New()
	uncompressedHash := sha256.New()
	counter := &countedWriter{w: io.MultiWriter(tmp, compressedHash)}
	gz := gzip.NewWriter(counter)
	gz.Header.ModTime = time.Unix(0, 0)
	tw := tar.NewWriter(io.MultiWriter(gz, uncompressedHash))

	entries := make(map[string]bool)
	for _, path := range paths {
		if path == "" || filepath.IsAbs(path) || strings.HasPrefix(filepath.Clean(path), "..") {
			return empty, "", fmt.Errorf("invalid image path %q", path)
		}
		for parent := filepath.Dir(path); parent != "."; parent = filepath.Dir(parent) {
			entries[parent] = true
		}
		entries[path] = false
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if entries[name] {
			err = tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(name) + "/", Typeflag: tar.TypeDir, Mode: 0755, ModTime: time.Unix(0, 0)})
		} else {
			err = addRegularFile(tw, filepath.Join(source, name), filepath.ToSlash(name))
		}
		if err != nil {
			return empty, "", err
		}
	}
	if err := tw.Close(); err != nil {
		return empty, "", err
	}
	if err := gz.Close(); err != nil {
		return empty, "", err
	}
	if err := tmp.Close(); err != nil {
		return empty, "", err
	}
	digest := "sha256:" + hex.EncodeToString(compressedHash.Sum(nil))
	if err := os.Rename(tmp.Name(), filepath.Join(layoutDir, "blobs", "sha256", strings.TrimPrefix(digest, "sha256:"))); err != nil {
		return empty, "", err
	}
	return descriptor{MediaType: layerType, Digest: digest, Size: counter.n},
		"sha256:" + hex.EncodeToString(uncompressedHash.Sum(nil)), nil
}

func addRegularFile(tw *tar.Writer, sourcePath, name string) error {
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", sourcePath)
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = name
	hdr.Uid, hdr.Gid = 0, 0
	hdr.Uname, hdr.Gname = "", ""
	hdr.ModTime = time.Unix(0, 0)
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	f, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

func writeJSONBlob(layoutDir, mediaType string, value any) (descriptor, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return descriptor{}, err
	}
	h := sha256.Sum256(b)
	digest := hex.EncodeToString(h[:])
	if err := os.WriteFile(filepath.Join(layoutDir, "blobs", "sha256", digest), b, 0644); err != nil {
		return descriptor{}, err
	}
	return descriptor{MediaType: mediaType, Digest: "sha256:" + digest, Size: int64(len(b))}, nil
}

func writeJSONFile(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0644)
}
