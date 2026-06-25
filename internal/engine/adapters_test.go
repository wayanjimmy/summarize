package engine

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAdaptersListModels(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "models")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\n' 'Models' '- one' '- two'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	want := []string{"one", "two"}
	if got, err := NewPiAdapter(bin).ListModels(context.Background()); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("pi ListModels() = %#v, %v", got, err)
	}
	if got, err := NewAgyAdapter(bin).ListModels(context.Background()); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("agy ListModels() = %#v, %v", got, err)
	}
}
