package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	repoPattern   = regexp.MustCompile(`^[a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*(\/[a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*)*$`)
	tagPattern    = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)
	digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	idPattern     = regexp.MustCompile(`^[a-f0-9]{32}$`)
)

const manifestType = "application/vnd.oci.image.manifest.v1+json"

type registry struct{ root string }
type descriptor struct {
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}
type manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        descriptor   `json:"config"`
	Layers        []descriptor `json:"layers"`
}

func main() {
	listen := flag.String("listen", "127.0.0.1:5000", "loopback listen address")
	data := flag.String("data", "dist/registry", "persistent registry data directory")
	flag.Parse()
	if flag.NArg() != 0 {
		log.Fatal("usage: registry [-listen 127.0.0.1:5000] [-data dist/registry]")
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("-listen must be a numeric loopback address, such as 127.0.0.1:5000")
	}
	root, err := filepath.Abs(*data)
	if err != nil {
		log.Fatal(err)
	}
	for _, dir := range []string{"blobs/sha256", "repos", "uploads"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			log.Fatal(err)
		}
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("OCI registry listening on http://%s; data: %s", ln.Addr(), root)
	log.Fatal(http.Serve(ln, &registry{root: root}))
}

func (s *registry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	if r.URL.Path == "/v2/" || r.URL.Path == "/v2" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodNotAllowed(w, "GET, HEAD")
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v2/")
	if path == r.URL.Path {
		apiError(w, 404, "UNSUPPORTED", "unknown endpoint")
		return
	}
	if strings.HasSuffix(path, "/tags/list") {
		s.tags(w, r, strings.TrimSuffix(path, "/tags/list"))
		return
	}
	if repo, ref, ok := strings.Cut(path, "/manifests/"); ok {
		s.manifests(w, r, repo, ref)
		return
	}
	if repo, rest, ok := strings.Cut(path, "/blobs/uploads/"); ok {
		s.uploads(w, r, repo, rest)
		return
	}
	if repo, digest, ok := strings.Cut(path, "/blobs/"); ok {
		s.blobs(w, r, repo, digest)
		return
	}
	apiError(w, 404, "UNSUPPORTED", "unknown endpoint")
}

func validRepo(repo string) bool     { return len(repo) <= 255 && repoPattern.MatchString(repo) }
func validRef(ref string) bool       { return tagPattern.MatchString(ref) || digestPattern.MatchString(ref) }
func digestHex(digest string) string { return strings.TrimPrefix(digest, "sha256:") }
func (s *registry) blobPath(digest string) string {
	return filepath.Join(s.root, "blobs", "sha256", digestHex(digest))
}
func (s *registry) tagPath(repo, tag string) string {
	return filepath.Join(s.root, "repos", filepath.FromSlash(repo), "tags", tag)
}

func (s *registry) blobs(w http.ResponseWriter, r *http.Request, repo, digest string) {
	if !validRepo(repo) || !digestPattern.MatchString(digest) {
		apiError(w, 400, "DIGEST_INVALID", "invalid repository or digest")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	file, err := os.Open(s.blobPath(digest))
	if errors.Is(err, os.ErrNotExist) {
		apiError(w, 404, "BLOB_UNKNOWN", "blob not found")
		return
	}
	if err != nil {
		apiError(w, 500, "UNKNOWN", err.Error())
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		apiError(w, 500, "UNKNOWN", err.Error())
		return
	}
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", info.ModTime(), file)
}

func (s *registry) uploads(w http.ResponseWriter, r *http.Request, repo, id string) {
	if !validRepo(repo) {
		apiError(w, 400, "NAME_INVALID", "invalid repository")
		return
	}
	if id == "" && r.Method == http.MethodPost {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			apiError(w, 500, "UNKNOWN", err.Error())
			return
		}
		id = hex.EncodeToString(raw[:])
		if err := os.WriteFile(filepath.Join(s.root, "uploads", id), nil, 0600); err != nil {
			apiError(w, 500, "UNKNOWN", err.Error())
			return
		}
		w.Header().Set("Location", "/v2/"+repo+"/blobs/uploads/"+id)
		w.Header().Set("Docker-Upload-UUID", id)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if !idPattern.MatchString(id) {
		apiError(w, 404, "BLOB_UPLOAD_UNKNOWN", "upload not found")
		return
	}
	upload := filepath.Join(s.root, "uploads", id)
	if r.Method == http.MethodDelete {
		if err := os.Remove(upload); err != nil {
			apiError(w, 404, "BLOB_UPLOAD_UNKNOWN", "upload not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPut {
		methodNotAllowed(w, "PUT, DELETE")
		return
	}
	digest := r.URL.Query().Get("digest")
	if !digestPattern.MatchString(digest) {
		apiError(w, 400, "DIGEST_INVALID", "invalid sha256 digest")
		return
	}
	file, err := os.OpenFile(upload, os.O_WRONLY|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		apiError(w, 409, "BLOB_UPLOAD_INVALID", "upload is in use")
		return
	}
	if err != nil {
		apiError(w, 404, "BLOB_UPLOAD_UNKNOWN", "upload not found")
		return
	}
	defer os.Remove(upload)
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(file, h), r.Body)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		apiError(w, 500, "UNKNOWN", "failed to write upload")
		return
	}
	if hex.EncodeToString(h.Sum(nil)) != digestHex(digest) {
		apiError(w, 400, "DIGEST_INVALID", "uploaded bytes do not match digest")
		return
	}
	if err := os.Rename(upload, s.blobPath(digest)); err != nil {
		apiError(w, 500, "UNKNOWN", err.Error())
		return
	}
	w.Header().Set("Location", "/v2/"+repo+"/blobs/"+digest)
	w.Header().Set("Docker-Content-Digest", digest)
	w.WriteHeader(http.StatusCreated)
}

func (s *registry) manifests(w http.ResponseWriter, r *http.Request, repo, ref string) {
	if !validRepo(repo) || !validRef(ref) {
		apiError(w, 400, "NAME_INVALID", "invalid repository or reference")
		return
	}
	if r.Method == http.MethodPut {
		if !tagPattern.MatchString(ref) {
			apiError(w, 400, "TAG_INVALID", "push requires a tag")
			return
		}
		s.putManifest(w, r, repo, ref)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD, PUT")
		return
	}
	digest := ref
	if !digestPattern.MatchString(ref) {
		data, err := os.ReadFile(s.tagPath(repo, ref))
		if err != nil {
			apiError(w, 404, "MANIFEST_UNKNOWN", "manifest not found")
			return
		}
		digest = strings.TrimSpace(string(data))
		if !digestPattern.MatchString(digest) {
			apiError(w, 500, "UNKNOWN", "corrupt tag reference")
			return
		}
	}
	file, err := os.Open(s.blobPath(digest))
	if err != nil {
		apiError(w, 404, "MANIFEST_UNKNOWN", "manifest not found")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		apiError(w, 500, "UNKNOWN", err.Error())
		return
	}
	w.Header().Set("Content-Type", manifestType)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Docker-Content-Digest", digest)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = io.Copy(w, file)
	}
}

func (s *registry) putManifest(w http.ResponseWriter, r *http.Request, repo, tag string) {
	if r.Header.Get("Content-Type") != manifestType {
		apiError(w, 415, "MANIFEST_INVALID", "expected OCI image manifest content type")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 4<<20+1))
	if err != nil {
		apiError(w, 400, "MANIFEST_INVALID", err.Error())
		return
	}
	if len(data) > 4<<20 {
		apiError(w, 413, "MANIFEST_INVALID", "manifest too large")
		return
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil || m.SchemaVersion != 2 || m.MediaType != manifestType {
		apiError(w, 400, "MANIFEST_INVALID", "invalid OCI image manifest")
		return
	}
	for _, d := range append([]descriptor{m.Config}, m.Layers...) {
		if !digestPattern.MatchString(d.Digest) || d.Size < 0 {
			apiError(w, 400, "MANIFEST_INVALID", "invalid descriptor")
			return
		}
		info, err := os.Stat(s.blobPath(d.Digest))
		if err != nil || info.Size() != d.Size {
			apiError(w, 400, "MANIFEST_BLOB_UNKNOWN", "referenced blob missing or wrong size")
			return
		}
	}
	h := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(h[:])
	if err := os.WriteFile(s.blobPath(digest), data, 0600); err != nil {
		apiError(w, 500, "UNKNOWN", err.Error())
		return
	}
	tagPath := s.tagPath(repo, tag)
	if err := os.MkdirAll(filepath.Dir(tagPath), 0700); err != nil {
		apiError(w, 500, "UNKNOWN", err.Error())
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(tagPath), ".tag-")
	if err != nil {
		apiError(w, 500, "UNKNOWN", err.Error())
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := io.WriteString(tmp, digest+"\n"); err != nil {
		tmp.Close()
		apiError(w, 500, "UNKNOWN", err.Error())
		return
	}
	if err := tmp.Close(); err != nil {
		apiError(w, 500, "UNKNOWN", err.Error())
		return
	}
	if err := os.Rename(tmp.Name(), tagPath); err != nil {
		apiError(w, 500, "UNKNOWN", err.Error())
		return
	}
	w.Header().Set("Location", "/v2/"+repo+"/manifests/"+digest)
	w.Header().Set("Docker-Content-Digest", digest)
	w.WriteHeader(http.StatusCreated)
}

func (s *registry) tags(w http.ResponseWriter, r *http.Request, repo string) {
	if !validRepo(repo) {
		apiError(w, 400, "NAME_INVALID", "invalid repository")
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	entries, err := os.ReadDir(filepath.Join(s.root, "repos", filepath.FromSlash(repo), "tags"))
	if errors.Is(err, os.ErrNotExist) {
		apiError(w, 404, "NAME_UNKNOWN", "repository not found")
		return
	}
	if err != nil {
		apiError(w, 500, "UNKNOWN", err.Error())
		return
	}
	tags := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && tagPattern.MatchString(entry.Name()) {
			tags = append(tags, entry.Name())
		}
	}
	sort.Strings(tags)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}{repo, tags})
}

func apiError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"code": code, "message": message}}})
}
func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	apiError(w, 405, "UNSUPPORTED", fmt.Sprintf("method not allowed; use %s", allow))
}
