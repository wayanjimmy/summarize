package workflow

import (
	"github.com/jimbo/summarize/internal/config"
	"github.com/jimbo/summarize/internal/engine"
	"github.com/jimbo/summarize/internal/store"
)

// Deps holds dependencies for workflow activities.
type Deps struct {
	Store     *store.Store
	Config    *config.Config
	PiEngine  engine.Engine
	AgyEngine engine.Engine
}

// deps is set at app startup before worker starts.
var deps Deps

// SetDeps sets the global activity dependencies.
func SetDeps(d Deps) {
	deps = d
}
