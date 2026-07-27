package server

import (
	"context"
	"fmt"

	"go.lsp.dev/protocol"
	"go.uber.org/zap"
)

func (s Server) TypeDefinition(ctx context.Context, params *protocol.TypeDefinitionParams) (result []protocol.Location, err error) {
	doc, err := s.docs.Read(ctx, params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	defer doc.Close()

	logger := protocol.LoggerFromContext(ctx).
		With(textDocumentFields(params.TextDocumentPositionParams)...)
	logger.Debug("type definition")

	positions := s.analyzer.TypeDefinition(ctx, doc, params.Position)
	logger.With(zap.Namespace("type_definition")).Debug(fmt.Sprintf("found type definition locations: %v", positions))

	return positions, nil
}
