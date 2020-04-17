package client

import (
	"io/ioutil"
	"os"
	"path"
	"testing"
)

func setupTestDir(t *testing.T) string {
	t.Helper()

	tmp, err := ioutil.TempDir("", "test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	files := []struct {
		name    string
		content string
	}{
		{
			name:    "1.txt",
			content: "hello there",
		},
		{
			name:    "2.sql",
			content: "SELECT 1;",
		},
		{
			name:    "3.txt",
			content: "general kenobi",
		},
	}

	for _, f := range files {
		if err := ioutil.WriteFile(path.Join(tmp, f.name), []byte(f.content), 0644); err != nil {
			t.Fatalf("failed to write test file %q: %v", f.name, err)
		}
	}

	return tmp
}

func cleanupTestDir(t *testing.T, path string) {
	t.Helper()

	if err := os.RemoveAll(path); err != nil {
		t.Logf("failed to remove test dir: %v", err)
	}
}
