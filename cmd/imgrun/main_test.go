package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMountBind(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(source, []byte("model"), 0600); err != nil {
		t.Fatal(err)
	}
	bind := bindMount{Source: source, Target: "/app/models/model.gguf"}
	if err := mountBind(root, bind); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "app/models/model.gguf"))
	if err != nil || string(got) != "model" {
		t.Fatalf("mounted file = %q, %v", got, err)
	}
	if err := mountBind(root, bind); err == nil {
		t.Fatal("expected existing target to be rejected")
	}
}

func TestMountBindRejectsSymlinkParent(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "app")); err != nil {
		t.Fatal(err)
	}
	if err := mountBind(root, bindMount{Source: source, Target: "/app/models"}); err == nil {
		t.Fatal("expected symlink parent to be rejected")
	}
}

func TestStopWhenParentExitsStopsWorkloadGroup(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	done := make(chan struct{})
	defer close(done)
	// A parent ID that is not ours behaves like a parent that already exited.
	go stopWhenParentExits(os.Getppid()+100000, -cmd.Process.Pid, done)
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("workload still running after its parent was gone")
	}
}

func TestStopWhenParentExitsWaitsWhileParentLives(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	done := make(chan struct{})
	go stopWhenParentExits(os.Getppid(), -cmd.Process.Pid, done)
	time.Sleep(3 * parentCheckInterval)
	close(done)
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("workload was stopped while its parent was alive: %v", err)
	}
}
