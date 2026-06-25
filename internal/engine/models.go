package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const DefaultModelCatalogTTL = 5 * time.Minute

var ErrNoModels = errors.New("model catalog returned no models")

// ModelCatalog caches model lists from engine CLIs.
type ModelCatalog struct {
	listers map[string]ModelLister
	ttl     time.Duration
	now     func() time.Time

	mu    sync.Mutex
	cache map[string]cachedModels
}

type cachedModels struct {
	models    []string
	fetchedAt time.Time
	stale     bool
	err       string
}

// EngineModels describes model availability for one engine.
type EngineModels struct {
	Models    []string
	FetchedAt time.Time
	Stale     bool
	Error     string
}

// NewModelCatalog creates a lazily fetched model catalog.
func NewModelCatalog(ttl time.Duration, listers ...ModelLister) *ModelCatalog {
	if ttl <= 0 {
		ttl = DefaultModelCatalogTTL
	}
	c := &ModelCatalog{
		listers: map[string]ModelLister{},
		ttl:     ttl,
		now:     time.Now,
		cache:   map[string]cachedModels{},
	}
	for _, l := range listers {
		if l != nil {
			c.listers[l.Name()] = l
		}
	}
	return c
}

// Get returns models for one engine, using cached data while fresh and stale data on refresh failure.
func (c *ModelCatalog) Get(ctx context.Context, engineName string) (EngineModels, error) {
	c.mu.Lock()
	entry, ok := c.cache[engineName]
	now := c.now()
	if ok && now.Sub(entry.fetchedAt) < c.ttl && entry.err == "" {
		c.mu.Unlock()
		return engineModels(entry), nil
	}
	lister := c.listers[engineName]
	c.mu.Unlock()

	if lister == nil {
		return EngineModels{}, fmt.Errorf("unknown engine %q", engineName)
	}

	models, err := lister.ListModels(ctx)
	fetchedAt := c.now()

	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil && len(models) == 0 {
		err = ErrNoModels
	}
	if err != nil {
		if ok && len(entry.models) > 0 {
			entry.stale = true
			entry.err = err.Error()
			c.cache[engineName] = entry
			return engineModels(entry), nil
		}
		entry = cachedModels{fetchedAt: fetchedAt, stale: false, err: err.Error()}
		c.cache[engineName] = entry
		return engineModels(entry), err
	}

	entry = cachedModels{models: models, fetchedAt: fetchedAt}
	c.cache[engineName] = entry
	return engineModels(entry), nil
}

// ListAll returns models for every registered engine, preserving partial failures in each engine status.
func (c *ModelCatalog) ListAll(ctx context.Context) map[string]EngineModels {
	c.mu.Lock()
	names := make([]string, 0, len(c.listers))
	for name := range c.listers {
		names = append(names, name)
	}
	c.mu.Unlock()

	out := make(map[string]EngineModels, len(names))
	for _, name := range names {
		models, err := c.Get(ctx, name)
		if err != nil && models.Error == "" {
			models.Error = err.Error()
		}
		out[name] = models
	}
	return out
}

func engineModels(c cachedModels) EngineModels {
	models := append([]string(nil), c.models...)
	if models == nil {
		models = []string{}
	}
	return EngineModels{Models: models, FetchedAt: c.fetchedAt, Stale: c.stale, Error: c.err}
}

// ParseModelList extracts model names from common CLI list formats.
func ParseModelList(out string) []string {
	seen := map[string]bool{}
	var models []string
	for _, line := range strings.Split(out, "\n") {
		model := parseModelLine(line)
		if model == "" || seen[model] {
			continue
		}
		seen[model] = true
		models = append(models, model)
	}
	return models
}

func parseModelLine(line string) string {
	s := strings.TrimSpace(line)
	if s == "" || strings.Trim(s, "-+|= ") == "" {
		return ""
	}
	lower := strings.ToLower(s)
	if lower == "models" || lower == "model" || strings.HasPrefix(lower, "available models") {
		return ""
	}

	s = strings.TrimLeft(s, "*•- ")
	if strings.Contains(s, "|") {
		parts := strings.Split(s, "|")
		hasHeader := false
		for _, part := range parts {
			candidate := strings.TrimSpace(part)
			if strings.EqualFold(candidate, "model") || strings.EqualFold(candidate, "name") {
				hasHeader = true
				break
			}
		}
		if hasHeader {
			return ""
		}
		for _, part := range parts {
			candidate := strings.TrimSpace(part)
			if candidate == "" || strings.EqualFold(candidate, "model") || strings.EqualFold(candidate, "name") || strings.EqualFold(candidate, "provider") {
				continue
			}
			return candidate
		}
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	if strings.EqualFold(fields[0], "model") || strings.EqualFold(fields[0], "name") {
		return ""
	}
	if len(fields) > 1 && (strings.Contains(fields[0], "/") || strings.Contains(fields[0], ":")) {
		return fields[0]
	}
	return strings.TrimSpace(s)
}
