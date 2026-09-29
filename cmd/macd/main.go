package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const apiVersion = "1.45"

type imageConfig struct {
	ID           string    `json:"id"`
	Created      time.Time `json:"created"`
	OS           string    `json:"os"`
	Architecture string    `json:"architecture"`
	Config       struct {
		Entrypoint []string `json:"Entrypoint"`
		Cmd        []string `json:"Cmd"`
		WorkingDir string   `json:"WorkingDir"`
		Env        []string `json:"Env"`
	} `json:"config"`
}

type frame struct {
	stream byte
	data   []byte
}

type bindMount struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type container struct {
	id      string
	name    string
	image   string
	created time.Time
	started time.Time
	status  string
	exit    int
	cmd     *exec.Cmd
	done    chan struct{}
	logs    []frame
	logSize int
	watch   map[chan frame]struct{}
	binds   []bindMount
	command []string
}

type daemon struct {
	mu           sync.Mutex
	containers   map[string]*container
	image        string
	imageID      string
	imageSize    int64
	imageCreated time.Time
	tag          string
	runner       string
	config       imageConfig
}

func main() {
	image := flag.String("image", "", "local Buildx OCI tarball")
	tag := flag.String("tag", "tiny-web:latest", "image name visible to Docker")
	runner := flag.String("runner", ".build/imgrun", "path to the native image runner")
	socket := flag.String("socket", "/private/tmp/macnative-docker.sock", "Docker Engine API Unix socket")
	flag.Parse()
	if *image == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: macd -image <buildx-oci.tar> [-tag tiny-web:latest] [-runner .build/imgrun] [-socket path]")
		os.Exit(2)
	}
	if err := serve(*image, *tag, *runner, *socket); err != nil {
		fmt.Fprintln(os.Stderr, "macd:", err)
		os.Exit(1)
	}
}

func serve(image, tag, runner, socket string) error {
	var err error
	image, err = filepath.Abs(image)
	if err != nil {
		return err
	}
	runner, err = filepath.Abs(runner)
	if err != nil {
		return err
	}
	if _, err := os.Stat(runner); err != nil {
		return fmt.Errorf("runner: %w", err)
	}
	sourceImage := image
	state, err := os.MkdirTemp("", "macnative-daemon-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(state)
	f, err := os.Open(sourceImage)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	image = filepath.Join(state, "image.tar")
	copyFile, err := os.OpenFile(image, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		f.Close()
		return err
	}
	if _, err := io.Copy(copyFile, f); err != nil {
		copyFile.Close()
		f.Close()
		return err
	}
	f.Close()
	if err := copyFile.Close(); err != nil {
		return err
	}
	cmd := exec.Command(runner, "-inspect", "-image", image)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("inspect image: %w", err)
	}
	var cfg imageConfig
	if err := json.Unmarshal(out, &cfg); err != nil {
		return err
	}
	if cfg.OS != "darwin" || cfg.Architecture != "arm64" {
		return fmt.Errorf("image is %s/%s, expected darwin/arm64", cfg.OS, cfg.Architecture)
	}
	if !strings.HasPrefix(cfg.ID, "sha256:") {
		return fmt.Errorf("invalid image config digest %q", cfg.ID)
	}
	d := &daemon{
		containers:   make(map[string]*container),
		image:        image,
		imageID:      cfg.ID,
		imageSize:    info.Size(),
		imageCreated: cfg.Created,
		tag:          tag,
		runner:       runner,
		config:       cfg,
	}
	if conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond); err == nil {
		conn.Close()
		return fmt.Errorf("Docker socket is already in use: %s", socket)
	}
	if err := os.Remove(socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(socket)
	if err := os.Chmod(socket, 0600); err != nil {
		return err
	}
	server := &http.Server{Handler: d}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		d.stopAll()
		_ = server.Close()
	}()
	fmt.Fprintf(os.Stderr, "macd: serving %s as %s on unix://%s\n", sourceImage, tag, socket)
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (d *daemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := unversionedPath(r.URL.Path)
	fmt.Fprintf(os.Stderr, "macd: %s %s\n", r.Method, p)
	w.Header().Set("API-Version", apiVersion)
	w.Header().Set("Docker-Experimental", "false")
	w.Header().Set("OSType", "darwin")
	switch {
	case p == "/_ping" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		w.Header().Set("Content-Type", "text/plain")
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "OK")
		}
	case p == "/version" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"Version": "0.1.0", "ApiVersion": apiVersion, "MinAPIVersion": "1.24",
			"GitCommit": "prototype", "GoVersion": runtime.Version(),
			"Os": "darwin", "Arch": "arm64", "KernelVersion": "macOS",
		})
	case p == "/info" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"ID": "macnative", "Name": "macnative", "OSType": "darwin",
			"Architecture": "arm64", "ServerVersion": "0.1.0",
			"Containers": len(d.containers), "Images": 1,
		})
	case p == "/images/json" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, []any{map[string]any{
			"Id": d.imageID, "RepoTags": []string{d.tag},
			"Size": d.imageSize, "Created": d.imageCreated.Unix(),
		}})
	case strings.HasPrefix(p, "/images/") && strings.HasSuffix(p, "/json") && r.Method == http.MethodGet:
		d.imageInspect(w, p)
	case p == "/containers/create" && r.Method == http.MethodPost:
		d.create(w, r)
	case p == "/containers/json" && r.Method == http.MethodGet:
		d.list(w)
	case strings.HasPrefix(p, "/containers/"):
		d.containerRequest(w, r, p)
	default:
		writeError(w, http.StatusNotImplemented, "Docker API endpoint is not implemented")
	}
}

func unversionedPath(p string) string {
	if !strings.HasPrefix(p, "/v") {
		return p
	}
	parts := strings.SplitN(strings.TrimPrefix(p, "/"), "/", 2)
	if len(parts) != 2 || !strings.Contains(parts[0], ".") {
		return p
	}
	if _, err := strconv.ParseFloat(strings.TrimPrefix(parts[0], "v"), 64); err != nil {
		return p
	}
	return "/" + parts[1]
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"message": message})
}

func (d *daemon) imageInspect(w http.ResponseWriter, p string) {
	name := strings.TrimSuffix(strings.TrimPrefix(p, "/images/"), "/json")
	if name != d.tag && name != d.imageID && name != strings.TrimSuffix(d.tag, ":latest") {
		writeError(w, http.StatusNotFound, "No such image: "+name)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"Id": d.imageID, "RepoTags": []string{d.tag}, "Size": d.imageSize,
		"Created": d.imageCreated.Format(time.RFC3339Nano),
		"Os":      "darwin", "Architecture": "arm64",
		"Config": map[string]any{
			"Entrypoint": d.config.Config.Entrypoint,
			"Cmd":        d.config.Config.Cmd,
			"WorkingDir": d.config.Config.WorkingDir,
			"Env":        d.config.Config.Env,
		},
	})
}

func newID() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func (d *daemon) create(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Image      string   `json:"Image"`
		Cmd        []string `json:"Cmd"`
		Entrypoint []string `json:"Entrypoint"`
		Tty        bool     `json:"Tty"`
		OpenStdin  bool     `json:"OpenStdin"`
		HostConfig struct {
			AutoRemove   bool            `json:"AutoRemove"`
			Binds        []string        `json:"Binds"`
			Mounts       json.RawMessage `json:"Mounts"`
			PortBindings json.RawMessage `json:"PortBindings"`
		} `json:"HostConfig"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if request.Image != d.tag && request.Image != strings.TrimSuffix(d.tag, ":latest") {
		writeError(w, http.StatusNotFound, "No such image: "+request.Image)
		return
	}
	if len(request.Entrypoint) != 0 || request.Tty || request.OpenStdin || request.HostConfig.AutoRemove || (len(request.HostConfig.Mounts) != 0 && string(request.HostConfig.Mounts) != "null" && string(request.HostConfig.Mounts) != "[]") || (len(request.HostConfig.PortBindings) != 0 && string(request.HostConfig.PortBindings) != "null" && string(request.HostConfig.PortBindings) != "{}") {
		writeError(w, http.StatusBadRequest, "this prototype supports read-only -v binds but not --mount, entrypoint overrides, TTY, stdin, port mappings, or --rm")
		return
	}
	binds := make([]bindMount, 0, len(request.HostConfig.Binds))
	for _, raw := range request.HostConfig.Binds {
		bind, err := parseBind(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		binds = append(binds, bind)
	}
	id, err := newID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "macnative-" + id[:12]
	}
	d.mu.Lock()
	for _, existing := range d.containers {
		if existing.name == name {
			d.mu.Unlock()
			writeError(w, http.StatusConflict, "container name already exists: "+name)
			return
		}
	}
	d.containers[id] = &container{
		id: id, name: name, image: d.tag, created: time.Now(),
		status: "created", done: make(chan struct{}), watch: make(map[chan frame]struct{}),
		binds: binds, command: append([]string(nil), request.Cmd...),
	}
	d.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"Id": id, "Warnings": []string{}})
}

func parseBind(raw string) (bindMount, error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 3 || parts[2] != "ro" || !filepath.IsAbs(parts[0]) || !filepath.IsAbs(parts[1]) || filepath.Clean(parts[1]) == "/" {
		return bindMount{}, fmt.Errorf("unsupported bind %q: use /absolute/host/path:/absolute/image/path:ro", raw)
	}
	info, err := os.Stat(parts[0])
	if err != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
		return bindMount{}, fmt.Errorf("bind source must be an existing file or directory: %q", parts[0])
	}
	return bindMount{Source: parts[0], Target: parts[1]}, nil
}

func (d *daemon) find(id string) *container {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c := d.containers[id]; c != nil {
		return c
	}
	for _, c := range d.containers {
		if c.name == id || strings.HasPrefix(c.id, id) {
			return c
		}
	}
	return nil
}

func (d *daemon) containerRequest(w http.ResponseWriter, r *http.Request, p string) {
	rest := strings.TrimPrefix(p, "/containers/")
	parts := strings.SplitN(rest, "/", 2)
	c := d.find(parts[0])
	if c == nil {
		writeError(w, http.StatusNotFound, "No such container: "+parts[0])
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	switch {
	case action == "start" && r.Method == http.MethodPost:
		d.start(w, c)
	case action == "attach" && r.Method == http.MethodPost:
		d.attach(w, r, c)
	case action == "wait" && r.Method == http.MethodPost:
		d.wait(w, r, c)
	case action == "json" && r.Method == http.MethodGet:
		d.inspect(w, c)
	case action == "logs" && r.Method == http.MethodGet:
		d.logs(w, r, c)
	case action == "stop" && r.Method == http.MethodPost:
		d.stop(w, c)
	case action == "kill" && r.Method == http.MethodPost:
		d.stop(w, c)
	case action == "" && r.Method == http.MethodDelete:
		d.remove(w, r, c)
	default:
		writeError(w, http.StatusNotImplemented, "container operation is not implemented")
	}
}

func (d *daemon) start(w http.ResponseWriter, c *container) {
	d.mu.Lock()
	if c.status != "created" {
		d.mu.Unlock()
		writeError(w, http.StatusNotModified, "container is not in created state")
		return
	}
	args := []string{"-image", d.image}
	for _, bind := range c.binds {
		encoded, err := json.Marshal(bind)
		if err != nil {
			d.mu.Unlock()
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		args = append(args, "-bind", string(encoded))
	}
	for _, arg := range c.command {
		args = append(args, "-cmd", arg)
	}
	cmd := exec.Command(d.runner, args...)
	cmd.Stdout = &logWriter{d: d, c: c, stream: 1}
	cmd.Stderr = &logWriter{d: d, c: c, stream: 2}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		d.mu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	c.cmd = cmd
	c.status = "running"
	c.started = time.Now()
	d.mu.Unlock()
	go func() {
		err := cmd.Wait()
		exitCode := 0
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}
		d.mu.Lock()
		c.status = "exited"
		c.exit = exitCode
		close(c.done)
		d.mu.Unlock()
	}()
	w.WriteHeader(http.StatusNoContent)
}

type logWriter struct {
	d      *daemon
	c      *container
	stream byte
}

func (l *logWriter) Write(p []byte) (int, error) {
	data := append([]byte(nil), p...)
	f := frame{stream: l.stream, data: data}
	l.d.mu.Lock()
	l.c.logs = append(l.c.logs, f)
	l.c.logSize += len(data)
	for l.c.logSize > 1<<20 && len(l.c.logs) > 0 {
		l.c.logSize -= len(l.c.logs[0].data)
		l.c.logs = l.c.logs[1:]
	}
	for ch := range l.c.watch {
		select {
		case ch <- f:
		default:
		}
	}
	l.d.mu.Unlock()
	return len(p), nil
}

func writeFrame(w io.Writer, f frame) error {
	var header [8]byte
	header[0] = f.stream
	binary.BigEndian.PutUint32(header[4:], uint32(len(f.data)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(f.data)
	return err
}

func (d *daemon) attach(w http.ResponseWriter, r *http.Request, c *container) {
	if r.URL.Query().Get("stdin") == "1" || r.URL.Query().Get("stdout") == "0" {
		writeError(w, http.StatusBadRequest, "stdin or disabled stdout is not supported")
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeError(w, http.StatusInternalServerError, "connection hijack is unavailable")
		return
	}
	ch := make(chan frame, 64)
	d.mu.Lock()
	history := append([]frame(nil), c.logs...)
	running := c.status != "exited"
	if running {
		c.watch[ch] = struct{}{}
	}
	d.mu.Unlock()
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		d.mu.Lock()
		delete(c.watch, ch)
		d.mu.Unlock()
		return
	}
	defer conn.Close()
	defer func() {
		d.mu.Lock()
		delete(c.watch, ch)
		d.mu.Unlock()
	}()
	if strings.EqualFold(r.Header.Get("Upgrade"), "tcp") {
		_, _ = io.WriteString(rw, "HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
	} else {
		_, _ = io.WriteString(rw, "HTTP/1.1 200 OK\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: close\r\n\r\n")
	}
	if err := rw.Flush(); err != nil {
		return
	}
	if r.URL.Query().Get("logs") == "1" {
		for _, f := range history {
			if err := writeFrame(conn, f); err != nil {
				return
			}
		}
	}
	if !running || r.URL.Query().Get("stream") == "0" {
		return
	}
	for {
		select {
		case f := <-ch:
			if err := writeFrame(conn, f); err != nil {
				return
			}
		case <-c.done:
			for {
				select {
				case f := <-ch:
					if err := writeFrame(conn, f); err != nil {
						return
					}
				default:
					return
				}
			}
		}
	}
}

func (d *daemon) wait(w http.ResponseWriter, r *http.Request, c *container) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	select {
	case <-c.done:
		d.mu.Lock()
		code := c.exit
		d.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"StatusCode": code, "Error": nil})
	case <-r.Context().Done():
	}
}

func (d *daemon) inspect(w http.ResponseWriter, c *container) {
	d.mu.Lock()
	status, exitCode, started := c.status, c.exit, c.started
	command := d.config.Config.Cmd
	if len(c.command) != 0 {
		command = c.command
	}
	binds := make([]string, 0, len(c.binds))
	for _, bind := range c.binds {
		binds = append(binds, bind.Source+":"+bind.Target+":ro")
	}
	pid := 0
	if c.cmd != nil && status == "running" {
		pid = c.cmd.Process.Pid
	}
	d.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"Id": c.id, "Name": "/" + c.name, "Image": d.imageID,
		"Created": c.created.Format(time.RFC3339Nano),
		"State": map[string]any{
			"Status": status, "Running": status == "running", "ExitCode": exitCode,
			"Pid": pid, "StartedAt": started.Format(time.RFC3339Nano),
		},
		"Config": map[string]any{
			"Image": c.image, "Entrypoint": d.config.Config.Entrypoint,
			"Cmd": command, "WorkingDir": d.config.Config.WorkingDir,
			"Env": d.config.Config.Env, "Tty": false,
		},
		"HostConfig":      map[string]any{"NetworkMode": "host", "Binds": binds},
		"NetworkSettings": map[string]any{"Ports": map[string]any{}},
	})
}

func (d *daemon) list(w http.ResponseWriter) {
	d.mu.Lock()
	items := make([]any, 0, len(d.containers))
	for _, c := range d.containers {
		if c.status != "running" {
			continue
		}
		items = append(items, map[string]any{
			"Id": c.id, "Names": []string{"/" + c.name}, "Image": c.image,
			"ImageID": d.imageID, "Command": strings.Join(d.config.Config.Entrypoint, " "),
			"Created": c.created.Unix(), "State": c.status,
			"Status": "Up", "Ports": []any{},
		})
	}
	d.mu.Unlock()
	writeJSON(w, http.StatusOK, items)
}

func (d *daemon) logs(w http.ResponseWriter, r *http.Request, c *container) {
	follow := r.URL.Query().Get("follow") == "1" || strings.EqualFold(r.URL.Query().Get("follow"), "true")
	ch := make(chan frame, 64)
	d.mu.Lock()
	history := append([]frame(nil), c.logs...)
	running := c.status != "exited"
	if follow && running {
		c.watch[ch] = struct{}{}
	}
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(c.watch, ch)
		d.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	for _, f := range history {
		if err := writeFrame(w, f); err != nil {
			return
		}
	}
	flush()
	if !follow || !running {
		return
	}
	for {
		select {
		case f := <-ch:
			if err := writeFrame(w, f); err != nil {
				return
			}
			flush()
		case <-c.done:
			for {
				select {
				case f := <-ch:
					if err := writeFrame(w, f); err != nil {
						return
					}
					flush()
				default:
					return
				}
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (d *daemon) stop(w http.ResponseWriter, c *container) {
	d.mu.Lock()
	if c.status != "running" || c.cmd == nil {
		d.mu.Unlock()
		w.WriteHeader(http.StatusNotModified)
		return
	}
	process := c.cmd.Process
	d.mu.Unlock()
	if err := process.Signal(syscall.SIGTERM); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	select {
	case <-c.done:
	case <-time.After(10 * time.Second):
		writeError(w, http.StatusInternalServerError, "process did not stop within 10 seconds")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d *daemon) remove(w http.ResponseWriter, r *http.Request, c *container) {
	force := r.URL.Query().Get("force") == "1" || strings.EqualFold(r.URL.Query().Get("force"), "true")
	d.mu.Lock()
	if c.status == "running" {
		if !force {
			d.mu.Unlock()
			writeError(w, http.StatusConflict, "stop container before removing it")
			return
		}
		process, done := c.cmd.Process, c.done
		d.mu.Unlock()
		if err := process.Signal(syscall.SIGUSR1); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			writeError(w, http.StatusInternalServerError, "process did not exit within 10 seconds")
			return
		}
		d.mu.Lock()
	}
	delete(d.containers, c.id)
	d.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (d *daemon) stopAll() {
	type target struct {
		process *os.Process
		done    <-chan struct{}
	}
	d.mu.Lock()
	targets := make([]target, 0)
	for _, c := range d.containers {
		if c.status == "running" && c.cmd != nil {
			targets = append(targets, target{process: c.cmd.Process, done: c.done})
		}
	}
	d.mu.Unlock()
	for _, t := range targets {
		_ = t.process.Signal(syscall.SIGTERM)
	}
	for _, t := range targets {
		select {
		case <-t.done:
		case <-time.After(10 * time.Second):
			fmt.Fprintf(os.Stderr, "macd: runner pid %d did not stop within 10 seconds\n", t.process.Pid)
		}
	}
}
