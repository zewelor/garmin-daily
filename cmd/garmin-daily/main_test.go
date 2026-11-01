package main

import (
	"context"
	"io"
	"testing"
)

func TestRejectCLIInputsBeforeIO(t *testing.T) {
	for _, args := range [][]string{nil, {"other"}, {"sync"}, {"sync", "--from", "2026-02-30", "--to", "2026-03-01"}, {"sync", "--from", "2026-10-25", "--to", "2026-10-24"}, {"migrate", "extra"}, {"sync", "--from", "2026-10-25", "--to", "2026-10-25"}} {
		if err := run(context.Background(), args, func(string) string { return "" }, io.Discard); err == nil {
			t.Fatalf("accepted invalid input %v", args)
		}
	}
}
