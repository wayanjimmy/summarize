package workflow

import (
	"github.com/wayanjimmy/summarize/internal/config"
	"github.com/wayanjimmy/summarize/internal/engine"
	"github.com/wayanjimmy/summarize/internal/store"
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
