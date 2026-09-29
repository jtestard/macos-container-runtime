package main

import (
	"archive/tar"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func pullTag(from, tag string) (string, error) {
	if from == "" {
		return "", fmt.Errorf("fromImage is required")
	}
	if strings.Contains(from, "@") {
		if tag != "" {
			return "", fmt.Errorf("tag cannot accompany a digest")
		}
		return from, nil
	}
	last := from[strings.LastIndex(from, "/")+1:]
	if strings.Contains(last, ":") {
		if tag != "" {
			return "", fmt.Errorf("image already has a tag")
		}
		return from, nil
	}
	if tag == "" {
		tag = "latest"
	}
	return from + ":" + tag, nil
}

func registryAuth(header string) (authn.Authenticator, error) {
	if header == "" {
		return nil, nil
	}
	var data []byte
	var err error
	for _, enc := range []*base64.Encoding{base64.URLEncoding, base64.RawURLEncoding, base64.StdEncoding, base64.RawStdEncoding} {
		data, err = enc.DecodeString(header)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("invalid X-Registry-Auth encoding")
	}
	var config authn.AuthConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("invalid X-Registry-Auth JSON")
	}
	if config == (authn.AuthConfig{}) {
		return nil, nil
	}
	return authn.FromConfig(config), nil
}

func (d *daemon) pull(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("fromSrc") != "" {
		writeError(w, http.StatusBadRequest, "fromSrc is not supported")
		return
	}
	if p := q.Get("platform"); p != "" && p != "darwin/arm64" {
		writeError(w, http.StatusBadRequest, "only darwin/arm64 is supported")
		return
	}
	tag, err := pullTag(q.Get("fromImage"), q.Get("tag"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ref, err := name.ParseReference(tag)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	auth, err := registryAuth(r.Header.Get("X-Registry-Auth"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	progress := func(status string) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	fail := func(err error) {
		_ = json.NewEncoder(w).Encode(map[string]any{"errorDetail": map[string]string{"message": err.Error()}, "error": err.Error()})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	progress("Pulling " + tag + " (darwin/arm64)")
	opts := []remote.Option{remote.WithContext(r.Context()), remote.WithPlatform(v1.Platform{OS: "darwin", Architecture: "arm64"})}
	if auth != nil {
		opts = append(opts, remote.WithAuth(auth))
	} else {
		opts = append(opts, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	}
	img, err := remote.Image(ref, opts...)
	if err != nil {
		fail(err)
		return
	}
	config, err := img.ConfigFile()
	if err != nil {
		fail(err)
		return
	}
	if config.OS != "darwin" || config.Architecture != "arm64" {
		fail(fmt.Errorf("registry image is %s/%s, expected darwin/arm64", config.OS, config.Architecture))
		return
	}
	manifestDigest, err := img.Digest()
	if err != nil {
		fail(err)
		return
	}
	blob := manifestDigest.Hex
	if !validBlob(blob) {
		fail(fmt.Errorf("unsupported manifest digest"))
		return
	}
	path := filepath.Join(d.store, "blobs", blob+".tar")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		progress("Downloading image layers")
		if err := d.downloadImage(img, path); err != nil {
			fail(err)
			return
		}
	} else if err != nil {
		fail(err)
		return
	}
	stored, err := inspectImage(d.runner, path, tag)
	if err != nil {
		fail(err)
		return
	}
	stored.blob = blob
	d.mu.Lock()
	previous := d.images[tag]
	d.images[tag] = stored
	err = d.saveIndexLocked()
	if err != nil {
		if previous == nil {
			delete(d.images, tag)
		} else {
			d.images[tag] = previous
		}
	}
	d.mu.Unlock()
	if err != nil {
		fail(err)
		return
	}
	progress("Status: Downloaded newer image for " + tag)
}

func (d *daemon) downloadImage(img v1.Image, destination string) error {
	temp, err := os.MkdirTemp(d.store, "pull-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	path, err := layout.Write(filepath.Join(temp, "layout"), empty.Index)
	if err != nil {
		return err
	}
	if err := path.AppendImage(img, layout.WithPlatform(v1.Platform{OS: "darwin", Architecture: "arm64"})); err != nil {
		return err
	}
	out, err := os.Create(filepath.Join(temp, "image.tar"))
	if err != nil {
		return err
	}
	tw := tar.NewWriter(out)
	err = filepath.Walk(string(path), func(file string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(string(path), file)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Mode: 0600, Size: info.Size(), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		in, err := os.Open(file)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, in)
		closeErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		tw.Close()
		out.Close()
		return err
	}
	if err := tw.Close(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	tempTar := filepath.Join(temp, "image.tar")
	if _, err := inspectImage(d.runner, tempTar, ""); err != nil {
		return err
	}
	return os.Rename(tempTar, destination)
}
