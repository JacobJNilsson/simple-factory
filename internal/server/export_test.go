package server

import (
	"context"
	"io"
	"time"

	"github.com/JacobJNilsson/simple-factory/internal/engine"
)

// ServeWithMaxLine is Serve with its own request size limit.
func ServeWithMaxLine(ctx context.Context, socket string, eng *engine.Engine, tick time.Duration, log io.Writer, maxLine int) error {
	return serve(ctx, socket, eng, tick, log, maxLine, nil)
}
