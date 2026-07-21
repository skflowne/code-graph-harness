// Package pathnorm converts between Windows-style and WSL/Linux-style
// absolute file paths, and between those host paths and file:// URIs.
//
// The daemon (internal/core, internal/lsp, ...) always runs on Linux/WSL,
// but a harness driving it (e.g. Claude Code) may run on native Windows and
// therefore pass Windows-style paths (`C:\Users\me\proj\a.ts`) or
// `\\wsl$\...` / `\\wsl.localhost\...` UNC paths. Every path that crosses
// into the daemon's core should be run through Normalize first so that
// internal state (internal/core.Location.File, LSP requests, etc.) always
// deals in one canonical form: an absolute POSIX-style path as seen from
// inside the WSL/Linux filesystem.
//
// Because the daemon builds and tests only on Linux, Windows-side path
// handling here is implemented with explicit string logic (not
// path/filepath, which is OS-native and would behave differently on Windows
// vs Linux) so behavior is deterministic regardless of the host OS running
// the tests. The Linux/WSL side uses the OS-independent "path" package,
// which always uses '/' semantics.
package pathnorm

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
)

// isASCIILetter reports whether b is an ASCII letter (A-Z, a-z). Drive
// letters and similar are restricted to ASCII by convention on Windows.
func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// isDriveLetterPath reports whether p looks like "C:" or "C:\..." / "C:/...".
func isDriveLetterPath(p string) bool {
	if len(p) < 2 {
		return false
	}
	if !isASCIILetter(p[0]) || p[1] != ':' {
		return false
	}
	if len(p) == 2 {
		return true // bare "C:"
	}
	return p[2] == '\\' || p[2] == '/'
}

// isUNCPath reports whether p starts with a Windows UNC prefix ("\\...").
func isUNCPath(p string) bool {
	return strings.HasPrefix(p, `\\`)
}

// IsWindowsPath reports whether p is written using Windows path syntax: a
// drive letter ("C:\..." / "C:/...") or a UNC path ("\\server\share\...",
// including "\\wsl$\..." and "\\wsl.localhost\..."). It does not inspect
// the filesystem or require the path to exist.
func IsWindowsPath(p string) bool {
	return isDriveLetterPath(p) || isUNCPath(p)
}

// convertUNCToWSL converts a "\\wsl$\<distro>\<rest>" or
// "\\wsl.localhost\<distro>\<rest>" UNC path to the corresponding absolute
// Linux path "/<rest>" inside that distro. Any other UNC server name
// (a real network share, for instance) is not something a WSL/Linux
// process can resolve, so it is reported as an error rather than guessed at.
func convertUNCToWSL(p string) (string, error) {
	trimmed := strings.TrimPrefix(p, `\\`)
	trimmed = strings.ReplaceAll(trimmed, `\`, "/")
	trimmed = strings.TrimPrefix(trimmed, "/") // tolerate "\\\\server\..." typos
	segments := strings.Split(trimmed, "/")
	if len(segments) < 2 || segments[0] == "" || segments[1] == "" {
		return "", fmt.Errorf("pathnorm: malformed UNC path %q", p)
	}
	server := strings.ToLower(segments[0])
	if server != "wsl$" && server != "wsl.localhost" {
		return "", fmt.Errorf("pathnorm: unsupported UNC path %q (only \\\\wsl$\\<distro>\\... and \\\\wsl.localhost\\<distro>\\... are supported)", p)
	}
	rest := segments[2:] // segments[1] is the distro name, discarded
	result := "/" + strings.Join(rest, "/")
	return path.Clean(result), nil
}

// WindowsToWSL converts a Windows-style absolute path to the equivalent
// absolute Linux path as seen from inside WSL.
//
//   - Drive letters: "C:\Users\me\proj\a.ts" and "C:/Users/me/proj/a.ts"
//     (any letter, either case, either separator, and mixed separators)
//     both become "/mnt/c/Users/me/proj/a.ts". The drive letter is
//     lower-cased to match the real /mnt/<drive> mount convention.
//   - UNC WSL paths: "\\wsl$\Ubuntu\home\me\a.ts" and
//     "\\wsl.localhost\Ubuntu\home\me\a.ts" both become "/home/me/a.ts"
//     (the distro name segment is stripped; this function does not
//     validate it against the running distro).
//   - Already-WSL/POSIX paths (starting with "/") are passed through
//     unchanged (after path.Clean), so callers can run any path through
//     this function unconditionally.
//
// Any other input (a relative path, a bare "C:", an unsupported UNC server,
// or an empty string) is reported as an error.
func WindowsToWSL(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("pathnorm: empty path")
	}

	if isUNCPath(p) {
		return convertUNCToWSL(p)
	}

	if strings.HasPrefix(p, "/") {
		// Already a POSIX/WSL-style absolute path: pass through.
		return path.Clean(p), nil
	}

	if isDriveLetterPath(p) {
		if len(p) == 2 {
			return "", fmt.Errorf("pathnorm: %q is a bare drive with no path component", p)
		}
		drive := strings.ToLower(string(p[0]))
		rest := p[2:]
		rest = strings.TrimPrefix(rest, `\`)
		rest = strings.TrimPrefix(rest, "/")
		rest = strings.ReplaceAll(rest, `\`, "/")
		result := path.Clean("/mnt/" + drive + "/" + rest)
		return result, nil
	}

	return "", fmt.Errorf("pathnorm: %q is neither a Windows path nor an absolute WSL path", p)
}

// splitMountDrive reports whether p is of the form "/mnt/<drive>" or
// "/mnt/<drive>/<rest>", returning the lower-case drive letter and the
// remainder (including its leading "/", or "" if there is none).
func splitMountDrive(p string) (drive string, rest string, ok bool) {
	const prefix = "/mnt/"
	if !strings.HasPrefix(p, prefix) {
		return "", "", false
	}
	rem := p[len(prefix):]
	if rem == "" || !isASCIILetter(rem[0]) {
		return "", "", false
	}
	if len(rem) > 1 && rem[1] != '/' {
		return "", "", false // e.g. "/mnt/cdrom", not a drive mount
	}
	drive = strings.ToLower(string(rem[0]))
	if len(rem) > 1 {
		rest = rem[1:]
	}
	return drive, rest, true
}

// WSLToWindows converts an absolute Linux/WSL path to the equivalent
// Windows-style path.
//
//   - "/mnt/<drive>/rest..." becomes "<DRIVE>:\rest..." (drive upper-cased,
//     separators become backslashes). "/mnt/c" and "C:\" round-trip.
//   - Any other absolute Linux path (e.g. "/home/me/a.ts", which is *inside*
//     the WSL filesystem and has no /mnt/<drive> equivalent) cannot be
//     turned into a drive-letter path. Windows can still reach it via the
//     "\\wsl.localhost\<distro>\..." UNC form, so this function builds
//     that instead, using the WSL_DISTRO_NAME environment variable to name
//     the distro. If that variable is unset, there is no reliable way to
//     name the distro, so an error is returned rather than guessing.
//   - Windows-style input (already IsWindowsPath) is accepted too: it is
//     first normalized via WindowsToWSL and then re-converted, which has
//     the effect of canonicalizing separators/drive-letter case.
//
// A non-absolute path is an error.
func WSLToWindows(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("pathnorm: empty path")
	}

	hostPath := p
	if IsWindowsPath(p) {
		converted, err := WindowsToWSL(p)
		if err != nil {
			return "", err
		}
		hostPath = converted
	}

	if !strings.HasPrefix(hostPath, "/") {
		return "", fmt.Errorf("pathnorm: %q is not an absolute path", p)
	}
	hostPath = path.Clean(hostPath)

	if drive, rest, ok := splitMountDrive(hostPath); ok {
		rest = strings.TrimPrefix(rest, "/")
		winPath := strings.ToUpper(drive) + `:\` + strings.ReplaceAll(rest, "/", `\`)
		return winPath, nil
	}

	distro := os.Getenv("WSL_DISTRO_NAME")
	if distro == "" {
		return "", fmt.Errorf("pathnorm: %q is not under /mnt/<drive>, and WSL_DISTRO_NAME is unset so a \\\\wsl.localhost\\<distro>\\... path cannot be built; set WSL_DISTRO_NAME or handle this path as WSL-internal", hostPath)
	}
	winSuffix := strings.ReplaceAll(hostPath, "/", `\`)
	return `\\wsl.localhost\` + distro + winSuffix, nil
}

// Normalize returns the canonical host (Linux/WSL) absolute path form of p,
// regardless of whether p was given as a Windows path, a WSL UNC path, or
// already a POSIX path. It is best-effort: unlike WindowsToWSL/WSLToWindows
// it never returns an error, because callers (e.g. deep in a request
// handling path) generally want a usable path back rather than a failure.
// When conversion via WindowsToWSL isn't possible (e.g. an unsupported UNC
// server), it falls back to a syntactic clean-up (backslashes flipped to
// forward slashes, then path.Clean).
//
// Normalize is idempotent: Normalize(Normalize(p)) == Normalize(p).
func Normalize(p string) string {
	if p == "" {
		return p
	}
	if IsWindowsPath(p) {
		if converted, err := WindowsToWSL(p); err == nil {
			return converted
		}
	}
	return path.Clean(strings.ReplaceAll(p, `\`, "/"))
}

// PathToURI converts an absolute host path (Windows or WSL/POSIX; it is run
// through Normalize first) to a file:// URI (RFC 8089), percent-encoding
// any characters (spaces, unicode, etc.) that require it.
func PathToURI(p string) string {
	hostPath := Normalize(p)
	u := url.URL{Scheme: "file", Path: hostPath}
	return u.String()
}

// URIToPath converts a file:// URI back to an absolute host path,
// percent-decoding as needed. It accepts both the canonical empty-authority
// form ("file:///mnt/c/...") and an explicit "localhost" authority.
//
// A file URI with a Windows-style path (e.g. "file:///C:/Users/me/a.ts",
// as produced by a Windows-side tool) is recognized and normalized to the
// host form via Normalize, so downstream code always sees one shape.
//
// A non-empty, non-"localhost" authority is treated as a UNC network path
// and rendered as "//<host><path>"; this is a best-effort fallback since a
// WSL/Linux process generally cannot resolve such a share directly.
func URIToPath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("pathnorm: invalid URI %q: %w", uri, err)
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("pathnorm: %q is not a file:// URI (scheme %q)", uri, u.Scheme)
	}
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		return "//" + u.Host + u.Path, nil
	}
	if u.Path == "" {
		return "", fmt.Errorf("pathnorm: %q has an empty path", uri)
	}

	p := u.Path
	// "file:///C:/Users/..." parses with a leading slash before the drive
	// letter; strip it before treating the rest as a Windows path.
	if len(p) >= 3 && p[0] == '/' && isASCIILetter(p[1]) && p[2] == ':' {
		return Normalize(p[1:]), nil
	}
	return Normalize(p), nil
}
