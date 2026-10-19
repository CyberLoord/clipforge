package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLimitAndCancellation(t *testing.T) {
	root := t.TempDir()
	local, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := local.Save(context.Background(), "job/video.mp4", strings.NewReader("too big"), 3); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := local.Save(ctx, "job/video.mp4", strings.NewReader("abc"), 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	files, err := os.ReadDir(filepath.Join(root, "job"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatal("temporary upload was not cleaned")
	}
	for _, key := range []string{"../secret", "/absolute", ""} {
		if _, err := local.Path(key); err == nil {
			t.Fatalf("accepted key %q", key)
		}
	}
}
