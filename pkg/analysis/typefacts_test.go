package analysis

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

func TestTypeFactsParameterCompletionAndDefinitions(t *testing.T) {
	f := newFixture(t)
	impl := f.File("impl.go", strings.Repeat("\n", 20))
	f.LoadTypeFacts(fmt.Sprintf(`{
  "version": "starlark-typefacts/1",
  "types": [
    {
      "id": "example.BotBuilder",
      "name": "BotBuilder",
      "location": {"uri": %q, "line": 1, "column": 1},
      "methods": [
        {
          "name": "screen",
          "location": {"uri": %q, "line": 10, "column": 5},
          "signature": {
            "params": [
              {"name": "id", "type": {"kind": "str"}},
              {
                "name": "handler",
                "type": {
                  "kind": "callback",
                  "params": [
                    {"name": "event", "type": {"id": "example.Event", "kind": "value", "name": "Event"}},
                    {"name": "screen", "type": {"id": "example.Screen", "kind": "value", "name": "Screen"}}
                  ]
                }
              }
            ]
          }
        }
      ]
    },
    {
      "id": "example.Event",
      "name": "Event",
      "methods": [{"name": "text", "signature": {"returns": {"kind": "str"}}}]
    },
    {
      "id": "example.Screen",
      "name": "Screen",
      "methods": [{"name": "respond", "signature": {"params": [{"name": "text", "type": {"kind": "str"}}]}}]
    }
  ],
  "facts": [
    {
      "files": ["Tiltfile.test"],
      "scope": {"kind": "function", "name": "configure"},
      "symbol": {"kind": "parameter", "name": "bot"},
      "type": {"id": "example.BotBuilder", "kind": "value", "name": "BotBuilder"}
    }
  ]
}`, string(uri.File(impl)), string(uri.File(impl))))

	botSource := `def configure(bot):
  bot.screen("start", start_screen)
`
	doc := f.MainDoc(botSource)

	result := f.a.Completion(doc, positionAfter(botSource, "bot."))
	assertCompletionResult(t, []string{"screen"}, result)

	methodPos := positionOn(botSource, "screen(\"start\"")
	definition := f.a.Definition(f.ctx, doc, methodPos)
	require.Len(t, definition, 1)
	require.Equal(t, uri.File(impl), definition[0].URI)
	require.Equal(t, protocol.Position{Line: 9, Character: 4}, definition[0].Range.Start)

	typeDefinition := f.a.TypeDefinition(f.ctx, doc, positionAfter(botSource, "bot"))
	require.Len(t, typeDefinition, 1)
	require.Equal(t, uri.File(impl), typeDefinition[0].URI)
	require.Equal(t, protocol.Position{Line: 0, Character: 0}, typeDefinition[0].Range.Start)

	callbackSource := `def start_screen(event, screen):
  screen.r

def configure(bot):
  bot.screen("start", start_screen)
`
	doc = f.MainDoc(callbackSource)
	result = f.a.Completion(doc, positionAfter(callbackSource, "screen."))
	assertCompletionResult(t, []string{"respond"}, result)
}

func TestTypeFactsAssignedVariableCompletionAndDefinitions(t *testing.T) {
	f := newFixture(t)
	impl := f.File("impl.go", strings.Repeat("\n", 40))
	f.LoadTypeFacts(fmt.Sprintf(`{
  "version": "starlark-typefacts/1",
  "types": [
    {
      "id": "example.BotBuilder",
      "name": "BotBuilder",
      "methods": [
        {
          "name": "screen",
          "signature": {
            "params": [
              {"name": "id", "type": {"kind": "str"}},
              {
                "name": "handler",
                "type": {
                  "kind": "callback",
                  "params": [
                    {"name": "event", "type": {"id": "example.Event", "kind": "value", "name": "Event"}},
                    {"name": "screen", "type": {"id": "example.Screen", "kind": "value", "name": "Screen"}}
                  ]
                }
              }
            ]
          }
        }
      ]
    },
    {
      "id": "example.Event",
      "name": "Event",
      "methods": [{"name": "text", "signature": {"returns": {"kind": "str"}}}]
    },
    {
      "id": "example.Screen",
      "name": "Screen",
      "methods": [
        {
          "name": "state",
          "signature": {"returns": {"id": "example.State", "kind": "value", "name": "State", "optional": true}}
        }
      ]
    },
    {
      "id": "example.State",
      "name": "State",
      "methods": [
        {"name": "delete", "location": {"uri": %q, "line": 20, "column": 5}, "signature": {"params": [{"name": "key", "type": {"kind": "str"}}]}},
        {"name": "get", "location": {"uri": %q, "line": 30, "column": 5}, "signature": {"params": [{"name": "key", "type": {"kind": "str"}}], "returns": {"kind": "str"}}}
      ]
    }
  ],
  "facts": [
    {
      "files": ["Tiltfile.test"],
      "scope": {"kind": "function", "name": "configure"},
      "symbol": {"kind": "parameter", "name": "bot"},
      "type": {"id": "example.BotBuilder", "kind": "value", "name": "BotBuilder"}
    }
  ]
}`, string(uri.File(impl)), string(uri.File(impl))))

	source := `def start_screen(event, screen):
  state = screen.state()
  state.delete("k")
  state.g

def configure(bot):
  bot.screen("start", start_screen)
`
	doc := f.MainDoc(source)

	result := f.a.Completion(doc, positionAfter(source, "state.g"))
	assertCompletionResult(t, []string{"get"}, result)

	definition := f.a.Definition(f.ctx, doc, positionOn(source, "delete"))
	require.Len(t, definition, 1)
	require.Equal(t, uri.File(impl), definition[0].URI)
	require.Equal(t, protocol.Position{Line: 19, Character: 4}, definition[0].Range.Start)
}

func (f *fixture) LoadTypeFacts(contents string) {
	f.t.Helper()
	path := filepath.Join(f.dir, "typefacts.json")
	require.NoError(f.t, os.WriteFile(path, []byte(contents), 0644))
	require.NoError(f.t, WithTypeFactPaths([]string{path})(f.a))
}

func positionAfter(source, needle string) protocol.Position {
	idx := strings.Index(source, needle)
	if idx < 0 {
		panic("needle not found: " + needle)
	}
	return offsetPosition(source, idx+len(needle))
}

func positionOn(source, needle string) protocol.Position {
	idx := strings.Index(source, needle)
	if idx < 0 {
		panic("needle not found: " + needle)
	}
	return offsetPosition(source, idx)
}

func offsetPosition(source string, offset int) protocol.Position {
	var line uint32
	var char uint32
	for i := 0; i < offset; i++ {
		if source[i] == '\n' {
			line++
			char = 0
			continue
		}
		char++
	}
	return protocol.Position{Line: line, Character: char}
}
