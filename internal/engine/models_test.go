package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestParseModelList(t *testing.T) {
	out := `Models
- cpa/tr-qwen3.7-max
• gemini:flash

| Model | Provider |
| --- | --- |
| claude-4 | anthropic |
cpa/tr-qwen3.7-max
`
	want := []string{"cpa/tr-qwen3.7-max", "gemini:flash", "claude-4"}
	if got := ParseModelList(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseModelList() = %#v, want %#v", got, want)
	}
}

func TestModelCatalogCacheAndStaleFallback(t *testing.T) {
	now := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	lister := &fakeLister{name: "pi", models: []string{"m1"}}
	c := NewModelCatalog(time.Minute, lister)
	c.now = func() time.Time { return now }

	got, err := c.Get(context.Background(), "pi")
	if err != nil || !reflect.DeepEqual(got.Models, []string{"m1"}) {
		t.Fatalf("first Get() = %#v, %v", got, err)
	}
	lister.models = []string{"m2"}
	got, err = c.Get(context.Background(), "pi")
	if err != nil || !reflect.DeepEqual(got.Models, []string{"m1"}) {
		t.Fatalf("cached Get() = %#v, %v", got, err)
	}

	now = now.Add(2 * time.Minute)
	lister.err = errors.New("boom")
	got, err = c.Get(context.Background(), "pi")
	if err != nil || !got.Stale || got.Error == "" || !reflect.DeepEqual(got.Models, []string{"m1"}) {
		t.Fatalf("stale Get() = %#v, %v", got, err)
	}
}

func TestModelCatalogEmptyListIsUnavailable(t *testing.T) {
	c := NewModelCatalog(time.Minute, &fakeLister{name: "pi", models: []string{}})
	got, err := c.Get(context.Background(), "pi")
	if !errors.Is(err, ErrNoModels) || got.Error == "" {
		t.Fatalf("empty Get() = %#v, %v", got, err)
	}
	if got.Models == nil || len(got.Models) != 0 {
		t.Fatalf("models = %#v, want empty non-nil slice", got.Models)
	}

	lister := &fakeLister{name: "agy", models: []string{"m1"}}
	now := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	c = NewModelCatalog(time.Minute, lister)
	c.now = func() time.Time { return now }
	if _, err := c.Get(context.Background(), "agy"); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	now = now.Add(2 * time.Minute)
	lister.models = nil
	got, err = c.Get(context.Background(), "agy")
	if err != nil || !got.Stale || !reflect.DeepEqual(got.Models, []string{"m1"}) {
		t.Fatalf("empty stale Get() = %#v, %v", got, err)
	}
}

type fakeLister struct {
	name   string
	models []string
	err    error
}

func (f *fakeLister) Name() string { return f.name }

func (f *fakeLister) ListModels(context.Context) ([]string, error) {
	return f.models, f.err
}
