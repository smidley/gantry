package docker

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// StreamLogs returns one container's stdout+stderr as a single, already-
// demuxed reader: name resolves to its current id via the registry (the
// same identity source Lookup/Running use), then the SDK's raw
// ContainerLogs stream -- multiplexed stdout/stderr frames when the
// container has no TTY, a single unstructured stream when it does -- is
// always run through stdcopy, which correctly passes a non-multiplexed
// stream through unchanged. An unknown name is reported as an error
// rather than reaching the SDK at all; the caller (the /api/containers/
// {name}/logs handler) maps that to 404 the same way it maps a real
// daemon error, so an unknown name and a currently-unreachable daemon
// look identical to callers.
func (c *Collector) StreamLogs(ctx context.Context, name string, follow bool, tail int) (io.ReadCloser, error) {
	m, ok := c.reg.lookupByName(name)
	if !ok {
		return nil, fmt.Errorf("unknown container %q", name)
	}

	raw, err := c.cli.ContainerLogs(ctx, m.ID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Tail:       strconv.Itoa(tail),
	})
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	go pumpLogs(ctx, raw, pw)
	return pr, nil
}

// pumpLogs demuxes raw into pw until whichever ends first: the
// underlying stream itself (EOF -- the normal non-follow outcome, once
// it's returned everything up to Tail) or ctx (the normal follow
// outcome, when the HTTP client disconnects). The SDK's ReadCloser gives
// no other way to interrupt a blocked Follow read than closing it, so
// the ctx-done path does exactly that, which then unblocks the demux
// goroutine's pending read with an error -- either way pw ends up
// closed, so the reader on the pipe's other end always sees a
// terminated stream, never a stall.
func pumpLogs(ctx context.Context, raw io.ReadCloser, pw *io.PipeWriter) {
	copyDone := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(pw, pw, raw)
		copyDone <- err
	}()
	select {
	case <-ctx.Done():
		_ = raw.Close()
		<-copyDone
		_ = pw.CloseWithError(ctx.Err())
	case err := <-copyDone:
		_ = raw.Close()
		_ = pw.CloseWithError(err)
	}
}
