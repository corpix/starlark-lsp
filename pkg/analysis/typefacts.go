package analysis

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/tilt-dev/starlark-lsp/pkg/document"
	"github.com/tilt-dev/starlark-lsp/pkg/query"
)

const TypeFactsVersion = "starlark-typefacts/1"

type typeFactStore struct {
	typesByID          map[string]query.Type
	typesByName        map[string]query.Type
	ambiguousTypeNames map[string]bool
	functions          map[string]query.Signature
	symbols            []query.Symbol
	facts              []symbolTypeFact
}

type typeFactsFile struct {
	Version   string             `json:"version"`
	Types     []typeFactType     `json:"types"`
	Functions []typeFactFunction `json:"functions"`
	Facts     []symbolTypeFact   `json:"facts"`
}

type typeFactType struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	StarlarkName string           `json:"starlark_name"`
	Doc          string           `json:"doc"`
	Deprecated   string           `json:"deprecated"`
	Location     typeFactLocation `json:"location"`
	Fields       []typeFactField  `json:"fields"`
	Methods      []typeFactMethod `json:"methods"`
	baseDir      string
}

type typeFactField struct {
	Name     string           `json:"name"`
	Type     typeFactRef      `json:"type"`
	Doc      string           `json:"doc"`
	Location typeFactLocation `json:"location"`
}

type typeFactMethod struct {
	Name       string            `json:"name"`
	Signature  typeFactSignature `json:"signature"`
	Doc        string            `json:"doc"`
	Deprecated string            `json:"deprecated"`
	Location   typeFactLocation  `json:"location"`
}

type typeFactFunction struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Signature typeFactSignature `json:"signature"`
	Doc       string            `json:"doc"`
	Location  typeFactLocation  `json:"location"`
	baseDir   string
}

type typeFactSignature struct {
	Params      []typeFactParam `json:"params"`
	Returns     *typeFactRef    `json:"returns"`
	ReturnTuple []typeFactRef   `json:"return_tuple"`
}

type typeFactParam struct {
	Name     string      `json:"name"`
	Type     typeFactRef `json:"type"`
	Variadic bool        `json:"variadic"`
	Optional bool        `json:"optional"`
}

type typeFactRef struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Name        string          `json:"name"`
	Package     string          `json:"package"`
	Optional    bool            `json:"optional"`
	HasContext  bool            `json:"has_context"`
	Params      []typeFactParam `json:"params"`
	Returns     *typeFactRef    `json:"returns"`
	ReturnTuple []typeFactRef   `json:"return_tuple"`
	Key         *typeFactRef    `json:"key"`
	Elem        *typeFactRef    `json:"elem"`
}

type typeFactLocation struct {
	URI    string `json:"uri"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

type symbolTypeFact struct {
	Files   []string         `json:"files"`
	Scope   typeFactSelector `json:"scope"`
	Symbol  typeFactSelector `json:"symbol"`
	Type    typeFactRef      `json:"type"`
	baseDir string
}

type typeFactSelector struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type lexicalScope struct {
	Kind string
	Name string
}

type analyzedTypeRef struct {
	ID   string
	Name string
}

type typeAnalysisMode int

const (
	typeAnalysisFull typeAnalysisMode = iota
	typeAnalysisNoCallbackFacts
)

func newTypeFactStore() *typeFactStore {
	return &typeFactStore{
		typesByID:          map[string]query.Type{},
		typesByName:        map[string]query.Type{},
		ambiguousTypeNames: map[string]bool{},
		functions:          map[string]query.Signature{},
		symbols:            []query.Symbol{},
		facts:              []symbolTypeFact{},
	}
}

func WithTypeFactPaths(paths []string) AnalyzerOption {
	return func(analyzer *Analyzer) error {
		for _, path := range paths {
			facts, err := loadTypeFacts(path)
			if err != nil {
				return err
			}
			if err := analyzer.typeFacts.merge(facts); err != nil {
				return err
			}
		}
		return nil
	}
}

func loadTypeFacts(path string) (*typeFactStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading type facts %s: %w", path, err)
	}
	var file typeFactsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parsing type facts %s: %w", path, err)
	}
	if file.Version != TypeFactsVersion {
		return nil, fmt.Errorf("type facts %s: unsupported version %q", path, file.Version)
	}
	baseDir := filepath.Dir(path)
	store := newTypeFactStore()
	for _, ty := range file.Types {
		ty.baseDir = baseDir
		qty := store.queryType(ty)
		if qty.Name == "" {
			return nil, fmt.Errorf("type facts %s: type is missing name", path)
		}
		if qty.ID != "" {
			store.typesByID[qty.ID] = qty
		}
		store.registerNamedType(qty)
	}
	for _, fn := range file.Functions {
		fn.baseDir = baseDir
		sig := store.querySignature(fn.Name, fn.Signature, fn.Location.toProtocolLocation(baseDir))
		store.functions[fn.Name] = sig
		store.symbols = append(store.symbols, sig.Symbol())
	}
	for _, fact := range file.Facts {
		fact.baseDir = baseDir
		store.facts = append(store.facts, fact)
	}
	return store, nil
}

func (s *typeFactStore) merge(other *typeFactStore) error {
	for id, ty := range other.typesByID {
		if existing, ok := s.typesByID[id]; ok {
			if existing.Name != ty.Name {
				return fmt.Errorf("type facts: duplicate type id %q names %q and %q", id, existing.Name, ty.Name)
			}
			continue
		}
		s.typesByID[id] = ty
	}
	for name, ty := range other.typesByName {
		if other.ambiguousTypeNames[name] {
			s.ambiguousTypeNames[name] = true
			delete(s.typesByName, name)
			continue
		}
		s.registerNamedType(ty)
	}
	for name, sig := range other.functions {
		s.functions[name] = sig
	}
	s.symbols = append(s.symbols, other.symbols...)
	s.facts = append(s.facts, other.facts...)
	return nil
}

func (s *typeFactStore) registerNamedType(ty query.Type) {
	if ty.Name == "" || s.ambiguousTypeNames[ty.Name] {
		return
	}
	if existing, ok := s.typesByName[ty.Name]; ok && existing.ID != ty.ID {
		delete(s.typesByName, ty.Name)
		s.ambiguousTypeNames[ty.Name] = true
		return
	}
	s.typesByName[ty.Name] = ty
}

func (s *typeFactStore) queryType(ty typeFactType) query.Type {
	out := query.Type{
		ID:       ty.ID,
		Name:     ty.Name,
		Location: ty.Location.toProtocolLocation(ty.baseDir),
	}
	for _, field := range ty.Fields {
		out.Fields = append(out.Fields, query.Symbol{
			Name:     field.Name,
			Kind:     protocol.SymbolKindField,
			TypeID:   field.Type.ID,
			TypeName: s.refTypeName(field.Type),
			Detail:   field.Doc,
			Location: field.Location.toProtocolLocation(ty.baseDir),
		})
	}
	out.Members = append(out.Members, out.Fields...)
	for _, method := range ty.Methods {
		sig := s.querySignature(method.Name, method.Signature, method.Location.toProtocolLocation(ty.baseDir))
		sig.Docs.Description = method.Doc
		out.Methods = append(out.Methods, sig)
		out.Members = append(out.Members, query.Symbol{
			Name:     method.Name,
			Kind:     protocol.SymbolKindMethod,
			Detail:   method.Doc,
			Location: method.Location.toProtocolLocation(ty.baseDir),
		})
	}
	return out
}

func (s *typeFactStore) querySignature(name string, sig typeFactSignature, loc protocol.Location) query.Signature {
	var params []query.Parameter
	for _, param := range sig.Params {
		params = append(params, s.queryParameter(param))
	}
	var returnType string
	var returnTypeID string
	if sig.Returns != nil {
		returnType = s.refTypeName(*sig.Returns)
		returnTypeID = sig.Returns.ID
	}
	return query.Signature{
		Name:         name,
		Params:       params,
		ReturnType:   returnType,
		ReturnTypeID: returnTypeID,
		Location:     loc,
	}
}

func (s *typeFactStore) queryParameter(param typeFactParam) query.Parameter {
	typeName := s.refTypeName(param.Type)
	name := param.Name
	if param.Variadic {
		name = "*" + name
	}
	content := name
	if typeName != "" {
		content += ": " + typeName
	}
	if param.Optional {
		content += " = None"
	}
	return query.Parameter{
		Name:           param.Name,
		TypeID:         param.Type.ID,
		TypeHint:       typeName,
		Content:        content,
		CallbackParams: s.queryCallbackParams(param.Type),
	}
}

func (s *typeFactStore) queryCallbackParams(ref typeFactRef) []query.Parameter {
	if ref.Kind != "callback" {
		return nil
	}
	params := make([]query.Parameter, 0, len(ref.Params))
	for _, param := range ref.Params {
		params = append(params, s.queryParameter(param))
	}
	return params
}

func (s *typeFactStore) refTypeName(ref typeFactRef) string {
	if ref.ID != "" {
		if ty, ok := s.typesByID[ref.ID]; ok {
			return ty.Name
		}
	}
	if ref.Name != "" {
		return ref.Name
	}
	switch ref.Kind {
	case "str", "string", "text":
		return "String"
	case "bytes":
		return "Bytes"
	case "bool":
		return "bool"
	case "int", "uint":
		return "int"
	case "float":
		return "float"
	case "list":
		return "List"
	case "map":
		return "Dict"
	case "callback":
		return "function"
	case "any", "starlark_value":
		return ""
	default:
		return ref.Kind
	}
}

func (s *typeFactStore) typeLocation(ref analyzedTypeRef) protocol.Location {
	if ref.ID != "" {
		if ty, ok := s.typesByID[ref.ID]; ok {
			return ty.Location
		}
	}
	if ref.Name != "" && !s.ambiguousTypeNames[ref.Name] {
		if ty, ok := s.typesByName[ref.Name]; ok {
			return ty.Location
		}
	}
	return protocol.Location{}
}

func (s *typeFactStore) typeByRef(ref analyzedTypeRef) (query.Type, bool) {
	if ref.ID != "" {
		ty, ok := s.typesByID[ref.ID]
		return ty, ok
	}
	if ref.Name != "" && !s.ambiguousTypeNames[ref.Name] {
		ty, ok := s.typesByName[ref.Name]
		return ty, ok
	}
	return query.Type{}, false
}

func (loc typeFactLocation) toProtocolLocation(baseDir string) protocol.Location {
	if loc.URI == "" && loc.File == "" {
		return protocol.Location{}
	}
	startLine := uint32(0)
	if loc.Line > 0 {
		startLine = uint32(loc.Line - 1)
	}
	startColumn := uint32(0)
	if loc.Column > 0 {
		startColumn = uint32(loc.Column - 1)
	}
	locationURI := uri.URI(loc.URI)
	if locationURI == "" {
		locationURI = uri.File(resolveTypeFactFile(baseDir, loc.File))
	}
	return protocol.Location{
		URI: locationURI,
		Range: protocol.Range{
			Start: protocol.Position{Line: startLine, Character: startColumn},
			End:   protocol.Position{Line: startLine, Character: startColumn + 1},
		},
	}
}

func resolveTypeFactFile(baseDir, file string) string {
	if filepath.IsAbs(file) {
		return filepath.Clean(file)
	}
	cwd, err := os.Getwd()
	if err == nil {
		candidate := filepath.Join(cwd, file)
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Clean(candidate)
		}
	}
	return filepath.Clean(filepath.Join(baseDir, file))
}

func scopeForNode(doc document.Document, node *sitter.Node) lexicalScope {
	if n := enclosingFunctionNode(node); n != nil {
		name := n.ChildByFieldName(query.FieldName)
		if name == nil {
			return lexicalScope{Kind: "function"}
		}
		return lexicalScope{Kind: "function", Name: doc.Content(name)}
	}
	return lexicalScope{Kind: "module"}
}

func enclosingFunctionNode(node *sitter.Node) *sitter.Node {
	for n := node; n != nil; n = n.Parent() {
		if n.Type() == query.NodeTypeFunctionDef {
			return n
		}
	}
	return nil
}

func (a *Analyzer) symbolsInScope(doc document.Document, node *sitter.Node) []query.Symbol {
	return a.symbolsInScopeWithMode(doc, node, typeAnalysisFull)
}

func (a *Analyzer) symbolsInScopeWithMode(doc document.Document, node *sitter.Node, mode typeAnalysisMode) []query.Symbol {
	symbols := query.SymbolsInScope(doc, node)
	scope := scopeForNode(doc, node)
	symbols = a.applySymbolFacts(doc, scope, symbols)
	if mode == typeAnalysisFull {
		symbols = a.applyCallbackFacts(doc, node, scope, symbols)
	}
	return symbols
}

func (a *Analyzer) documentSymbols(doc document.Document) []query.Symbol {
	return a.applySymbolFacts(doc, lexicalScope{Kind: "module"}, doc.Symbols())
}

func (a *Analyzer) applySymbolFacts(doc document.Document, scope lexicalScope, symbols []query.Symbol) []query.Symbol {
	if len(a.typeFacts.facts) == 0 {
		return symbols
	}
	out := append([]query.Symbol{}, symbols...)
	for i, sym := range out {
		for _, fact := range a.typeFacts.facts {
			if !fact.matches(doc, scope, sym) {
				continue
			}
			out[i].TypeID = fact.Type.ID
			out[i].TypeName = a.typeFacts.refTypeName(fact.Type)
		}
	}
	return out
}

func (a *Analyzer) applyCallbackFacts(doc document.Document, node *sitter.Node, scope lexicalScope, symbols []query.Symbol) []query.Symbol {
	if scope.Kind != "function" || scope.Name == "" {
		return symbols
	}
	fnNode := enclosingFunctionNode(node)
	if fnNode == nil {
		return symbols
	}
	fnSig := query.ExtractSignature(doc, fnNode)
	callbackParams := a.callbackParamsForFunction(doc, scope.Name)
	if len(callbackParams) == 0 {
		return symbols
	}
	out := append([]query.Symbol{}, symbols...)
	for i, cbParam := range callbackParams {
		if i >= len(fnSig.Params) {
			break
		}
		target := fnSig.Params[i].Name
		for j, sym := range out {
			if sym.Name != target {
				continue
			}
			out[j].TypeID = cbParam.TypeID
			out[j].TypeName = cbParam.TypeHint
			if out[j].Detail == "" {
				out[j].Detail = cbParam.Content
			}
		}
	}
	return out
}

func (a *Analyzer) callbackParamsForFunction(doc document.Document, name string) []query.Parameter {
	var result []query.Parameter
	queryCallNodes(doc.Tree().RootNode(), func(call *sitter.Node) {
		if len(result) > 0 {
			return
		}
		params := a.callbackParamsFromCall(doc, call, name)
		if len(params) > 0 {
			result = params
		}
	})
	return result
}

func queryCallNodes(node *sitter.Node, visit func(*sitter.Node)) {
	if node == nil {
		return
	}
	if node.Type() == query.NodeTypeCall {
		visit(node)
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		queryCallNodes(node.NamedChild(i), visit)
	}
}

func (a *Analyzer) callbackParamsFromCall(doc document.Document, call *sitter.Node, callbackName string) []query.Parameter {
	fn := call.ChildByFieldName("function")
	argsNode := call.ChildByFieldName("arguments")
	if fn == nil || argsNode == nil {
		return nil
	}
	sig, found := a.signatureInformationWithMode(
		doc,
		call,
		callWithArguments{fnName: doc.Content(fn), argsNode: argsNode},
		typeAnalysisNoCallbackFacts,
	)
	if !found {
		return nil
	}
	for _, arg := range callArguments(doc, argsNode) {
		if arg.identifier != callbackName {
			continue
		}
		param, ok := callArgumentParameter(sig, arg)
		if ok && len(param.CallbackParams) > 0 {
			return param.CallbackParams
		}
	}
	return nil
}

type callArgument struct {
	name       string
	identifier string
	position   int
}

func callArguments(doc document.Document, argsNode *sitter.Node) []callArgument {
	var out []callArgument
	positional := 0
	for i := 0; i < int(argsNode.NamedChildCount()); i++ {
		child := argsNode.NamedChild(i)
		if child == nil {
			continue
		}
		if child.Type() == query.NodeTypeKeywordArgument {
			nameNode := child.ChildByFieldName("name")
			valueNode := child.ChildByFieldName("value")
			if valueNode == nil && child.NamedChildCount() > 0 {
				valueNode = child.NamedChild(int(child.NamedChildCount()) - 1)
			}
			arg := callArgument{position: -1}
			if nameNode != nil {
				arg.name = doc.Content(nameNode)
			}
			if valueNode != nil && valueNode.Type() == query.NodeTypeIdentifier {
				arg.identifier = doc.Content(valueNode)
			}
			out = append(out, arg)
			continue
		}
		arg := callArgument{position: positional}
		if child.Type() == query.NodeTypeIdentifier {
			arg.identifier = doc.Content(child)
		}
		out = append(out, arg)
		positional++
	}
	return out
}

func callArgumentParameter(sig query.Signature, arg callArgument) (query.Parameter, bool) {
	if arg.name != "" {
		for _, param := range sig.Params {
			if param.Name == arg.name {
				return param, true
			}
		}
		return query.Parameter{}, false
	}
	if arg.position >= 0 && arg.position < len(sig.Params) {
		return sig.Params[arg.position], true
	}
	return query.Parameter{}, false
}

func (fact symbolTypeFact) matches(doc document.Document, scope lexicalScope, sym query.Symbol) bool {
	if !fact.matchesFile(doc.URI()) || !fact.Scope.matchesScope(scope) || !fact.Symbol.matchesSymbol(sym) {
		return false
	}
	return fact.Type.ID != "" || fact.Type.Name != "" || fact.Type.Kind != ""
}

func (fact symbolTypeFact) matchesFile(docURI uri.URI) bool {
	if len(fact.Files) == 0 {
		return true
	}
	filename, ok := uriFilename(docURI)
	if !ok {
		return false
	}
	abs, err := filepath.Abs(filename)
	if err != nil {
		abs = filename
	}
	cwd, _ := os.Getwd()
	values := []string{filepath.ToSlash(abs)}
	if cwd != "" {
		if rel, err := filepath.Rel(cwd, abs); err == nil {
			values = append(values, filepath.ToSlash(rel))
		}
	}
	if fact.baseDir != "" {
		if rel, err := filepath.Rel(fact.baseDir, abs); err == nil {
			values = append(values, filepath.ToSlash(rel))
		}
	}
	for _, pattern := range fact.Files {
		pattern = filepath.ToSlash(pattern)
		if filepath.IsAbs(pattern) {
			if globMatch(pattern, filepath.ToSlash(abs)) {
				return true
			}
			continue
		}
		for _, value := range values {
			if globMatch(pattern, value) {
				return true
			}
		}
	}
	return false
}

func (sel typeFactSelector) matchesScope(scope lexicalScope) bool {
	if sel.Kind != "" && sel.Kind != scope.Kind {
		return false
	}
	if sel.Name != "" && sel.Name != scope.Name {
		return false
	}
	return true
}

func (sel typeFactSelector) matchesSymbol(sym query.Symbol) bool {
	if sel.Name != "" && sel.Name != sym.Name {
		return false
	}
	switch sel.Kind {
	case "", "symbol", "variable", "parameter":
		return true
	case "function":
		return sym.Kind == protocol.SymbolKindFunction || sym.Kind == protocol.SymbolKindMethod
	case "field":
		return sym.Kind == protocol.SymbolKindField
	default:
		return false
	}
}

func uriFilename(u uri.URI) (string, bool) {
	defer func() {
		_ = recover()
	}()
	if u == "" {
		return "", false
	}
	return u.Filename(), true
}

func globMatch(pattern, value string) bool {
	re, err := globRegexp(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(value)
}

func globRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '/':
			b.WriteByte('/')
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
