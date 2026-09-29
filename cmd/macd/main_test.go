package main

import (
	"path/filepath"
	"testing"
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
