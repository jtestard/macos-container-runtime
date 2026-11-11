package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStartIsRefusedOnceShutdownBegins(t *testing.T) {
	d := &daemon{containers: map[string]*container{}, images: map[string]*storedImage{}}
	d.stopAll() // nothing running; marks the daemon as closing
	c := &container{id: "c1", status: "created", img: &storedImage{path: "/nonexistent.tar"}, done: make(chan struct{})}
	d.containers[c.id] = c
	rec := httptest.NewRecorder()
	d.start(rec, c)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("start during shutdown returned %d, want 503", rec.Code)
	}
	if c.status != "created" || c.cmd != nil {
		t.Fatalf("container was started during shutdown: status %q", c.status)
	}
}

func TestWaitRunnersReturnsOnlyRunnersStillRunning(t *testing.T) {
	stopped, running, alsoStopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	close(stopped)
	close(alsoStopped)
	runners := []runnerProcess{{done: stopped}, {done: running}, {done: alsoStopped}}
	start := time.Now()
	left := waitRunners(runners, 50*time.Millisecond)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("waitRunners took %v", elapsed)
	}
	if len(left) != 1 || left[0].done != running {
		t.Fatalf("got %d runners left, want only the running one", len(left))
	}
	if left := waitRunners(runners[:1], time.Second); len(left) != 0 {
		t.Fatalf("stopped runner reported as running")
	}
}
