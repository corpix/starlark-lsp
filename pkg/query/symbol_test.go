package query_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/protocol"

	"github.com/tilt-dev/starlark-lsp/pkg/query"
)

func TestQueryDocumentSymbols(t *testing.T) {
	f := newQueryFixture(t, "", `
x = a(3)
y = None
z = True
`)

	doc := f.document()
	symbols := query.DocumentSymbols(doc)
	names := make([]string, len(symbols))
	for i, sym := range symbols {
		names[i] = sym.Name
	}
	assert.Equal(t, []string{"x", "y", "z"}, names)
}

func TestQueryDocumentSymbolsExcludesClasses(t *testing.T) {
	f := newQueryFixture(t, "", `
class Foo:
  pass

bar = Foo
`)

	doc := f.document()
	symbols := query.DocumentSymbols(doc)
	names := make([]string, len(symbols))
	for i, sym := range symbols {
		names[i] = sym.Name
	}
	assert.Equal(t, []string{"bar"}, names)
}

func TestQueryDocumentSymbolsIncludesClassesWhenEnabled(t *testing.T) {
	f := newQueryFixture(t, "", `
class Foo:
  name: str
  def bar(self):
    pass

bar = Foo
`)

	doc := f.document()
	symbols := query.DocumentSymbols(doc, query.IncludeClassSymbols())
	names := make([]string, len(symbols))
	for i, sym := range symbols {
		names[i] = sym.Name
	}
	assert.Equal(t, []string{"Foo", "bar"}, names)
	require.Len(t, symbols[0].Children, 2)
	assert.Equal(t, []string{"name", "bar"}, []string{symbols[0].Children[0].Name, symbols[0].Children[1].Name})
	assert.Equal(t, "String", symbols[0].Children[0].TypeName)
}

func TestQueryDocumentSymbolsTypeNames(t *testing.T) {
	f := newQueryFixture(t, "", `
link: Link = None
names: List[str] = []
flag: bool = True
unknown: Union[str, Blob] = ""
`)

	doc := f.document()
	symbols := query.DocumentSymbols(doc)
	require.Len(t, symbols, 4)
	assert.Equal(t, "Link", symbols[0].TypeName)
	assert.Equal(t, protocol.SymbolKindVariable, symbols[0].Kind)
	assert.Equal(t, "List", symbols[1].TypeName)
	assert.Equal(t, protocol.SymbolKindArray, symbols[1].Kind)
	assert.Equal(t, "bool", symbols[2].TypeName)
	assert.Equal(t, protocol.SymbolKindBoolean, symbols[2].Kind)
	assert.Equal(t, "", symbols[3].TypeName)
}

func TestQuerySiblingSymbols(t *testing.T) {
	f := newQueryFixture(t, "", `
def foo():
  bar = 1
  def baz():
    pass
  # position
  pass

def start():
  pass
`)

	doc := f.document()
	n, ok := query.NamedNodeAtPosition(doc, protocol.Position{Line: 5, Character: 2})
	assert.True(t, ok)
	symbols := query.SiblingSymbols(doc, n.Parent().NamedChild(0), nil)
	names := make([]string, len(symbols))
	for i, sym := range symbols {
		names[i] = sym.Name
	}
	assert.Equal(t, []string{"bar", "baz"}, names)
}

func TestSymbolsInScope(t *testing.T) {
	f := newQueryFixture(t, "", `
def foo():
  bar = 1
  def baz():
    pass
  # position
  pass

def start():
  pass
`)

	doc := f.document()
	n, ok := query.NamedNodeAtPosition(doc, protocol.Position{Line: 5, Character: 2})
	assert.True(t, ok)
	symbols := query.SymbolsInScope(doc, n)
	names := make([]string, len(symbols))
	for i, sym := range symbols {
		names[i] = sym.Name
	}
	assert.Equal(t, []string{"bar", "baz"}, names)
}

func TestSymbolsInScopeExcludesFollowingSiblings(t *testing.T) {
	f := newQueryFixture(t, "", `
def foo():
  bar = 1
  def baz():
    pass
  # position
  quux = True
  return

def start():
  pass
`)

	doc := f.document()
	n, ok := query.NamedNodeAtPosition(doc, protocol.Position{Line: 5, Character: 2})
	assert.True(t, ok)
	symbols := query.SymbolsInScope(doc, n)
	names := make([]string, len(symbols))
	for i, sym := range symbols {
		names[i] = sym.Name
	}
	assert.Equal(t, []string{"bar", "baz"}, names)
}

func TestSymbolsInScopeIncludesFunctionArguments1(t *testing.T) {
	f := newQueryFixture(t, "", `
def foo(a, b=True, c=None):
  bar = 1
  def baz(d):
    pass
  # position
  pass
`)

	doc := f.document()
	n, ok := query.NamedNodeAtPosition(doc, protocol.Position{Line: 5, Character: 2})
	assert.True(t, ok)
	symbols := query.SymbolsInScope(doc, n)
	names := make([]string, len(symbols))
	for i, sym := range symbols {
		names[i] = sym.Name
	}
	assert.Equal(t, []string{"bar", "baz", "a", "b", "c"}, names)
}

func TestSymbolsInScopeIncludesFunctionArguments2(t *testing.T) {
	f := newQueryFixture(t, "", `
def foo(a, b=True, c=None):
  bar = 1
  def baz(d):
    # position
    pass
  pass
`)

	doc := f.document()
	n, ok := query.NamedNodeAtPosition(doc, protocol.Position{Line: 4, Character: 4})
	assert.True(t, ok)
	symbols := query.SymbolsInScope(doc, n)
	names := make([]string, len(symbols))
	for i, sym := range symbols {
		names[i] = sym.Name
	}
	assert.Equal(t, []string{"d", "bar", "baz", "a", "b", "c"}, names)
}
