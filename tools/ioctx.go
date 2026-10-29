package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// ioChunk is the most file I/O done between cancellation checks.
const ioChunk = 1 << 20

// copyCtx copies src to dst in ioChunk pieces, stopping with ctx's error
// between pieces once ctx is done.
func copyCtx(ctx context.Context, dst io.Writer, src io.Reader) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := io.CopyN(dst, src, ioChunk)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// readFileCtx is os.ReadFile that stops between chunks when ctx is done.
func readFileCtx(ctx context.Context, path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var buf bytes.Buffer
	if info, err := f.Stat(); err == nil {
		buf.Grow(int(info.Size()))
	}
	if err := copyCtx(ctx, &buf, f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// cancelled returns the error a file tool reports when ctx ended its call,
// so the executor records the call as cancelled rather than failed.
func cancelled(ctx context.Context, tool string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return true, fmt.Errorf("%s: cancelled: %w", tool, err)
	}
	return false, nil
}
