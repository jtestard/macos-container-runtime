package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type storedImage struct {
	tag     string
	path    string
	blob    string
	id      string
	size    int64
	created time.Time
	config  imageConfig
}

type imageIndexDisk struct {
	Tags map[string]string `json:"tags"`
}

func newDaemon(runner, store string) (*daemon, error) {
	if store == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		store = filepath.Join(base, "macnative", "images")
	}
	var err error
	store, err = filepath.Abs(store)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(store, "blobs"), 0700); err != nil {
		return nil, err
	}
	d := &daemon{containers: make(map[string]*container), images: make(map[string]*storedImage), runner: runner, store: store}
	data, err := os.ReadFile(filepath.Join(store, "index.json"))
	if os.IsNotExist(err) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	var index imageIndexDisk
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("image store index: %w", err)
	}
	for tag, blob := range index.Tags {
		if !validBlob(blob) {
			return nil, fmt.Errorf("invalid stored image blob %q", blob)
		}
		img, err := inspectImage(runner, filepath.Join(store, "blobs", blob+".tar"), tag)
		if err != nil {
			return nil, fmt.Errorf("stored image %s: %w", tag, err)
		}
		img.blob = blob
		d.images[tag] = img
	}
	return d, nil
}

func validBlob(blob string) bool {
	if len(blob) != 64 {
		return false
	}
	for _, c := range blob {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func inspectImage(runner, path, tag string) (*storedImage, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	out, err := exec.Command(runner, "-inspect", "-image", path).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("imgrun inspect: %v: %s", err, strings.TrimSpace(string(out)))
	}
	var config imageConfig
	if err := json.Unmarshal(out, &config); err != nil {
		return nil, err
	}
	if config.OS != "darwin" || config.Architecture != "arm64" {
		return nil, fmt.Errorf("expected darwin/arm64 image")
	}
	return &storedImage{tag: tag, path: path, id: config.ID, size: info.Size(), created: config.Created, config: config}, nil
}

func (d *daemon) addInitialImage(source, tag, state string) error {
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	path := filepath.Join(state, "startup-image.tar")
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	img, err := inspectImage(d.runner, path, tag)
	if err != nil {
		return err
	}
	d.images[tag] = img
	return nil
}

func (d *daemon) imageForLocked(name string) *storedImage {
	if img := d.images[name]; img != nil {
		return img
	}
	if !strings.Contains(name[strings.LastIndex(name, "/")+1:], ":") && !strings.Contains(name, "@") {
		if img := d.images[name+":latest"]; img != nil {
			return img
		}
	}
	for _, img := range d.images {
		if img.id == name {
			return img
		}
	}
	return nil
}

func (d *daemon) listImages(w http.ResponseWriter) {
	d.mu.Lock()
	tags := make([]string, 0, len(d.images))
	for tag := range d.images {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	items := make([]any, 0, len(tags))
	for _, tag := range tags {
		img := d.images[tag]
		items = append(items, map[string]any{"Id": img.id, "RepoTags": []string{tag}, "Size": img.size, "Created": img.created.Unix()})
	}
	d.mu.Unlock()
	writeJSON(w, http.StatusOK, items)
}

func (d *daemon) saveIndexLocked() error {
	index := imageIndexDisk{Tags: make(map[string]string)}
	for tag, img := range d.images {
		if img.blob != "" {
			index.Tags[tag] = img.blob
		}
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(d.store, "index-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), filepath.Join(d.store, "index.json"))
}
