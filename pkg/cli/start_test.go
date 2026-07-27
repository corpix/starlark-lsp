package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStartCommandHasCompletionBuiltinFallbackFlag(t *testing.T) {
	cmd := newStartCmd("starlark-lsp", nil)
	require.NotNil(t, cmd.Command.Flag("completion-builtin-fallback"))
}

func TestStartCommandHasTypeFactsFlag(t *testing.T) {
	cmd := newStartCmd("starlark-lsp", nil)
	require.NotNil(t, cmd.Command.Flag("typefacts"))
}
