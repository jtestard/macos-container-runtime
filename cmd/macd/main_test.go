package main

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParseBind(t *testing.T) {
	source := t.TempDir()
	bind, err := parseBind(source + ":/app/models:ro")
	if err != nil || bind.Source != source || bind.Target != "/app/models" {
		t.Fatalf("parseBind = %+v, %v", bind, err)
	}
	for _, raw := range []string{
		source + ":/app/models:rw",
		source + ":relative:ro",
		source + ":/:ro",
		filepath.Join(source, "missing") + ":/app/models:ro",
	} {
		if _, err := parseBind(raw); err == nil {
			t.Errorf("expected invalid bind %q to fail", raw)
		}
	}
}

func TestStartupImages(t *testing.T) {
	var images startupImages
	if err := images.Set("llama-server:local=/tmp/llama=server.tar"); err != nil {
		t.Fatal(err)
	}
	if err := images.Set("whisper:local=/tmp/whisper.tar"); err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 || images[0].tag != "llama-server:local" || images[0].path != "/tmp/llama=server.tar" || images[1].tag != "whisper:local" {
		t.Fatalf("startup images = %+v", images)
	}
	for _, invalid := range []string{"", "missing-equals", "=/tmp/image.tar", "tag="} {
		if err := images.Set(invalid); err == nil {
			t.Errorf("accepted invalid image %q", invalid)
		}
	}
}

func TestTailFramesPreservesStreamsAcrossSplitLines(t *testing.T) {
	history := []frame{
		{stream: 1, data: []byte("first\nsec")},
		{stream: 2, data: []byte("ond\nthird")},
		{stream: 1, data: []byte("\n")},
	}
	for _, tt := range []struct {
		count int
		want  []frame
	}{
		{count: 0, want: nil},
		{count: 1, want: []frame{{stream: 2, data: []byte("third")}, {stream: 1, data: []byte("\n")}}},
		{count: 2, want: []frame{{stream: 1, data: []byte("sec")}, {stream: 2, data: []byte("ond\nthird")}, {stream: 1, data: []byte("\n")}}},
		{count: 4, want: history},
	} {
		if got := tailFrames(history, tt.count); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("tailFrames(%d) = %#v, want %#v", tt.count, got, tt.want)
		}
	}
}

type logStreamRecorder struct {
	header http.Header
	chunks chan []byte
}

func (w *logStreamRecorder) Header() http.Header { return w.header }
func (w *logStreamRecorder) WriteHeader(int)     {}
func (w *logStreamRecorder) Flush()              {}
func (w *logStreamRecorder) Write(p []byte) (int, error) {
	w.chunks <- append([]byte(nil), p...)
	return len(p), nil
}

func TestLogsTailAndFollow(t *testing.T) {
	c := &container{
		status: "running",
		done:   make(chan struct{}),
		logs:   []frame{{stream: 1, data: []byte("old\nrecent\n")}},
		watch:  make(map[chan frame]struct{}),
	}
	d := &daemon{}
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/?follow=1&tail=1", nil)
	writer := &logStreamRecorder{header: make(http.Header), chunks: make(chan []byte, 4)}
	done := make(chan struct{})
	go func() {
		d.logs(writer, request, c)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()
	readChunk := func() []byte {
		t.Helper()
		select {
		case chunk := <-writer.chunks:
			return chunk
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for log stream")
			return nil
		}
	}
	readFrame := func() string {
		t.Helper()
		header := readChunk()
		data := readChunk()
		if len(header) != 8 || uint32(len(data)) != binary.BigEndian.Uint32(header[4:]) {
			t.Fatalf("invalid Docker log frame: header %v, data %q", header, data)
		}
		return string(data)
	}
	if got := readFrame(); got != "recent\n" {
		t.Fatalf("initial tail = %q", got)
	}
	d.mu.Lock()
	for watcher := range c.watch {
		watcher <- frame{stream: 2, data: []byte("new\n")}
	}
	d.mu.Unlock()
	if got := readFrame(); got != "new\n" {
		t.Fatalf("followed log = %q", got)
	}
}
