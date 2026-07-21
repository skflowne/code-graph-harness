package lsp

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// uriFromPath converts an absolute host path into a file:// URI. It is a
// small, local helper — deliberately not the shared internal/pathnorm
// package (built in parallel), since Phase 0 only needs to handle Linux
// absolute paths. Using net/url gets percent-encoding of special characters
// (spaces, etc.) for free.
func uriFromPath(absPath string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(absPath)}
	return u.String()
}

// pathFromURI converts a file:// URI back into a host absolute path.
func pathFromURI(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("lsp: parsing uri %q: %w", uri, err)
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("lsp: unsupported uri scheme %q in %q", u.Scheme, uri)
	}
	return u.Path, nil
}

// languageIDForFile picks the LSP languageId for didOpen based on extension.
func languageIDForFile(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".tsx":
		return "typescriptreact"
	case ".jsx":
		return "javascriptreact"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	default:
		return "typescript"
	}
}
