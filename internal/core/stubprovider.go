package core

import "context"

// StubProvider is a trivial LanguageProvider used for wiring, compilation, and
// unit tests of layers above the real LSP provider (notably the MCP server and
// tool handlers). Replace with internal/lsp.Provider at daemon startup.
//
// Fields let a test preload canned responses.
type StubProvider struct {
	Definitions map[string][]Location // keyed by file
	Refs        map[string][]Location
	Symbols     map[string][]Symbol
}

func (s *StubProvider) Definition(_ context.Context, file string, _ Position) ([]Location, error) {
	return s.Definitions[file], nil
}

func (s *StubProvider) References(_ context.Context, file string, _ Position, _ bool) ([]Location, error) {
	return s.Refs[file], nil
}

func (s *StubProvider) DocumentSymbols(_ context.Context, file string) ([]Symbol, error) {
	return s.Symbols[file], nil
}

func (s *StubProvider) Close() error { return nil }

// compile-time assertion that StubProvider satisfies the interface.
var _ LanguageProvider = (*StubProvider)(nil)
