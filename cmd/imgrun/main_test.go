package main

import (
	"os"
	"path/filepath"
	"testing"
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
