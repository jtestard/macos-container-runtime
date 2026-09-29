package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type descriptor struct {
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations"`
}
type index struct {
	Manifests []descriptor `json:"manifests"`
}
type manifest struct {
	Config descriptor   `json:"config"`
	Layers []descriptor `json:"layers"`
}

func main() {
	layout := flag.String("layout", "dist/go-inf-server-smollm2", "OCI image layout")
	registry := flag.String("registry", "http://127.0.0.1:5000", "loopback registry URL")
	repository := flag.String("repository", "go-inf-server", "repository name")
	tag := flag.String("tag", "smollm2", "tag")
	flag.Parse()
	if flag.NArg() != 0 {
		fail(fmt.Errorf("usage: imgpush [-layout path] [-registry http://127.0.0.1:5000] [-repository name] [-tag tag]"))
	}
	u, err := url.Parse(*registry)
	if err != nil || u.Scheme != "http" || u.Host != "127.0.0.1:5000" || u.Path != "" {
		fail(fmt.Errorf("registry must be http://127.0.0.1:5000"))
	}
	if *repository != "go-inf-server" || *tag != "smollm2" {
		fail(fmt.Errorf("this first image push supports go-inf-server:smollm2"))
	}
	if err := push(*layout, *registry, *repository, *tag); err != nil {
		fail(err)
	}
}
func fail(err error) { fmt.Fprintln(os.Stderr, "imgpush:", err); os.Exit(1) }

func push(layout, base, repo, tag string) error {
	client := &http.Client{Timeout: 20 * time.Minute}
	indexData, err := os.ReadFile(filepath.Join(layout, "index.json"))
	if err != nil {
		return err
	}
	var idx index
	if err := json.Unmarshal(indexData, &idx); err != nil {
		return err
	}
	if len(idx.Manifests) != 1 {
		return fmt.Errorf("expected one manifest, got %d", len(idx.Manifests))
	}
	manifestData, err := readBlob(layout, idx.Manifests[0])
	if err != nil {
		return err
	}
	var m manifest
	if err := json.Unmarshal(manifestData, &m); err != nil {
		return err
	}
	for _, d := range append([]descriptor{m.Config}, m.Layers...) {
		if err := pushBlob(client, layout, base, repo, d); err != nil {
			return err
		}
	}
	path := base + "/v2/" + repo + "/manifests/" + tag
	req, err := http.NewRequest(http.MethodPut, path, strings.NewReader(string(manifestData)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		return responseError(res)
	}
	if res.Header.Get("Docker-Content-Digest") != idx.Manifests[0].Digest {
		return fmt.Errorf("registry manifest digest differs from local image")
	}
	fmt.Printf("published %s/v2/%s/manifests/%s\n", base, repo, tag)
	fmt.Printf("image digest %s\n", idx.Manifests[0].Digest)
	return nil
}

func pushBlob(client *http.Client, layout, base, repo string, d descriptor) error {
	path := base + "/v2/" + repo + "/blobs/" + d.Digest
	req, err := http.NewRequest(http.MethodHead, path, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode == http.StatusOK {
		if res.Header.Get("Docker-Content-Digest") != d.Digest || res.ContentLength != d.Size {
			return fmt.Errorf("existing blob has wrong metadata: %s", d.Digest)
		}
		fmt.Printf("already present %s\n", d.Digest)
		return nil
	}
	if res.StatusCode != http.StatusNotFound {
		return responseError(res)
	}
	localPath, err := blobPath(layout, d)
	if err != nil {
		return err
	}
	if err := verifyFile(localPath, d); err != nil {
		return err
	}
	post, err := http.NewRequest(http.MethodPost, base+"/v2/"+repo+"/blobs/uploads/", nil)
	if err != nil {
		return err
	}
	res, err = client.Do(post)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		return responseError(res)
	}
	location := res.Header.Get("Location")
	if location == "" {
		return fmt.Errorf("registry did not return upload location")
	}
	u, err := url.Parse(location)
	if err != nil {
		return err
	}
	baseURL, _ := url.Parse(base)
	u = baseURL.ResolveReference(u)
	if u.Scheme != baseURL.Scheme || u.Host != baseURL.Host {
		return fmt.Errorf("registry returned foreign upload location")
	}
	q := u.Query()
	q.Set("digest", d.Digest)
	u.RawQuery = q.Encode()
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()
	put, err := http.NewRequest(http.MethodPut, u.String(), file)
	if err != nil {
		return err
	}
	put.ContentLength = d.Size
	put.Header.Set("Content-Type", "application/octet-stream")
	res, err = client.Do(put)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		return responseError(res)
	}
	if res.Header.Get("Docker-Content-Digest") != d.Digest {
		return fmt.Errorf("registry accepted wrong blob digest")
	}
	fmt.Printf("uploaded %s (%d bytes)\n", d.Digest, d.Size)
	return nil
}

func blobPath(layout string, d descriptor) (string, error) {
	if !strings.HasPrefix(d.Digest, "sha256:") || len(d.Digest) != 71 {
		return "", fmt.Errorf("unsupported digest %q", d.Digest)
	}
	hexPart := strings.TrimPrefix(d.Digest, "sha256:")
	if _, err := hex.DecodeString(hexPart); err != nil {
		return "", err
	}
	return filepath.Join(layout, "blobs", "sha256", hexPart), nil
}
func readBlob(layout string, d descriptor) ([]byte, error) {
	path, err := blobPath(layout, d)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != d.Size {
		return nil, fmt.Errorf("wrong size for %s", d.Digest)
	}
	h := sha256.Sum256(data)
	if "sha256:"+hex.EncodeToString(h[:]) != d.Digest {
		return nil, fmt.Errorf("wrong digest for %s", d.Digest)
	}
	return data, nil
}
func verifyFile(path string, d descriptor) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	h := sha256.New()
	n, err := io.Copy(h, file)
	if err != nil {
		return err
	}
	if n != d.Size || "sha256:"+hex.EncodeToString(h.Sum(nil)) != d.Digest {
		return fmt.Errorf("blob does not match descriptor: %s", d.Digest)
	}
	return nil
}
func responseError(res *http.Response) error {
	if res == nil {
		return errors.New("no registry response")
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
	return fmt.Errorf("registry returned %s: %s", res.Status, strings.TrimSpace(string(body)))
}
