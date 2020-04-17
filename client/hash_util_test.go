package client

import (
	"path"
	"testing"
)

func TestHashUtilGetDirectoryHash(t *testing.T) {
	t.Parallel()

	tmp := setupTestDir(t)
	defer cleanupTestDir(t, tmp)

	hash, err := GetDirectoryHash(tmp)
	if err != nil {
		t.Fatalf("failed to get directory hash: %v", err)
	}

	expected := "3c01b387636699191fdd281c78fcce8d"
	if hash != expected {
		t.Errorf("invalid directory hash, got %q, want %q", hash, expected)
	}
}

func TestHashUtilGetFileHash(t *testing.T) {
	t.Parallel()

	tmp := setupTestDir(t)
	defer cleanupTestDir(t, tmp)

	hash, err := GetFileHash(path.Join(tmp, "2.sql"))
	if err != nil {
		t.Fatalf("failed to get file hash: %v", err)
	}

	expected := "71568061b2970a4b7c5160fe75356e10"
	if hash != expected {
		t.Errorf("invalid file hash, got %q, want %q", hash, expected)
	}
}

func TestHashUtilGetTemplateHash(t *testing.T) {
	t.Parallel()

	tmp := setupTestDir(t)
	defer cleanupTestDir(t, tmp)

	hash, err := GetTemplateHash(tmp, path.Join(tmp, "2.sql"))
	if err != nil {
		t.Fatalf("failed to get template hash: %v", err)
	}

	expected := "addb861f5dc3ea8f45908a8b3cf7f969"
	if hash != expected {
		t.Errorf("invalid template hash, got %q, want %q", hash, expected)
	}
}
