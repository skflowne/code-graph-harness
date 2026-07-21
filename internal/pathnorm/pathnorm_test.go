package pathnorm

import (
	"strings"
	"testing"
)

func TestIsWindowsPath(t *testing.T) {
	cases := []struct {
		name string
		p    string
		want bool
	}{
		{"drive backslash", `C:\Users\me\proj\a.ts`, true},
		{"drive forward slash", `C:/Users/me/proj/a.ts`, true},
		{"lowercase drive", `c:\Users\me`, true},
		{"bare drive", `C:`, true},
		{"unc wsl$", `\\wsl$\Ubuntu\home\me\a.ts`, true},
		{"unc wsl.localhost", `\\wsl.localhost\Ubuntu\home\me\a.ts`, true},
		{"unc generic", `\\server\share\file.ts`, true},
		{"posix path", `/mnt/c/Users/me/a.ts`, false},
		{"posix home path", `/home/me/proj/a.ts`, false},
		{"relative path", `Users\me\proj\a.ts`, false},
		{"empty", ``, false},
		{"single letter no colon", `C\Users`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsWindowsPath(c.p); got != c.want {
				t.Errorf("IsWindowsPath(%q) = %v, want %v", c.p, got, c.want)
			}
		})
	}
}

func TestWindowsToWSL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"drive backslash uppercase", `C:\Users\me\proj\a.ts`, `/mnt/c/Users/me/proj/a.ts`, false},
		{"drive forward slash", `C:/Users/me/proj/a.ts`, `/mnt/c/Users/me/proj/a.ts`, false},
		{"drive lowercase letter", `d:\code\repo\file.go`, `/mnt/d/code/repo/file.go`, false},
		{"mixed separators", `C:\Users/me\proj/a.ts`, `/mnt/c/Users/me/proj/a.ts`, false},
		{"drive root only", `C:\`, `/mnt/c`, false},
		{"unc wsl$", `\\wsl$\Ubuntu\home\me\proj\a.ts`, `/home/me/proj/a.ts`, false},
		{"unc wsl.localhost", `\\wsl.localhost\Ubuntu\home\me\proj\a.ts`, `/home/me/proj/a.ts`, false},
		{"unc wsl$ root", `\\wsl$\Ubuntu`, `/`, false},
		{"unc wsl$ forward slashes tolerated", `\\wsl$\Ubuntu/home/me/a.ts`, `/home/me/a.ts`, false},
		{"already wsl passthrough", `/mnt/c/Users/me/proj/a.ts`, `/mnt/c/Users/me/proj/a.ts`, false},
		{"already posix home passthrough", `/home/me/proj/a.ts`, `/home/me/proj/a.ts`, false},
		{"posix path needing clean", `/home/me//proj/../proj/a.ts`, `/home/me/proj/a.ts`, false},
		{"empty", ``, ``, true},
		{"bare drive no path", `C:`, ``, true},
		{"relative path errors", `Users\me\a.ts`, ``, true},
		{"unsupported unc server", `\\server\share\file.ts`, ``, true},
		{"malformed unc", `\\wsl$`, ``, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := WindowsToWSL(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("WindowsToWSL(%q) = %q, nil; want error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("WindowsToWSL(%q) unexpected error: %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("WindowsToWSL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestWSLToWindows(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		distro  string // set WSL_DISTRO_NAME; "" leaves it unset
		want    string
		wantErr bool
	}{
		{"mnt drive", `/mnt/c/Users/me/proj/a.ts`, "", `C:\Users\me\proj\a.ts`, false},
		{"mnt drive root", `/mnt/c`, "", `C:\`, false},
		{"mnt drive uppercase letter normalized", `/mnt/D/code/repo/file.go`, "", `D:\code\repo\file.go`, false},
		{"non mnt path with distro set", `/home/me/proj/a.ts`, "Ubuntu", `\\wsl.localhost\Ubuntu\home\me\proj\a.ts`, false},
		{"non mnt path without distro errors", `/home/me/proj/a.ts`, "", ``, true},
		{"windows-style input re-canonicalized", `c:/Users/me\a.ts`, "", `C:\Users\me\a.ts`, false},
		{"empty", ``, "", ``, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.distro != "" {
				t.Setenv("WSL_DISTRO_NAME", c.distro)
			} else {
				t.Setenv("WSL_DISTRO_NAME", "")
			}
			got, err := WSLToWindows(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("WSLToWindows(%q) = %q, nil; want error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("WSLToWindows(%q) unexpected error: %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("WSLToWindows(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestRoundTrip checks WindowsToWSL <-> WSLToWindows composes to the
// identity (up to canonicalization) in both directions.
func TestRoundTrip(t *testing.T) {
	t.Run("windows to wsl to windows", func(t *testing.T) {
		windowsPaths := []string{
			`C:\Users\me\proj\a.ts`,
			`d:\code\repo\file.go`,
			`E:\a\b\c.txt`,
		}
		for _, wp := range windowsPaths {
			wsl, err := WindowsToWSL(wp)
			if err != nil {
				t.Fatalf("WindowsToWSL(%q): %v", wp, err)
			}
			back, err := WSLToWindows(wsl)
			if err != nil {
				t.Fatalf("WSLToWindows(%q): %v", wsl, err)
			}
			if !strings.EqualFold(back, wp) {
				t.Errorf("round trip %q -> %q -> %q, want case-insensitive match with original", wp, wsl, back)
			}
		}
	})

	t.Run("wsl to windows to wsl for mnt paths", func(t *testing.T) {
		wslPaths := []string{
			`/mnt/c/Users/me/proj/a.ts`,
			`/mnt/d/code/repo/file.go`,
		}
		for _, p := range wslPaths {
			win, err := WSLToWindows(p)
			if err != nil {
				t.Fatalf("WSLToWindows(%q): %v", p, err)
			}
			back, err := WindowsToWSL(win)
			if err != nil {
				t.Fatalf("WindowsToWSL(%q): %v", win, err)
			}
			if back != p {
				t.Errorf("round trip %q -> %q -> %q, want %q", p, win, back, p)
			}
		}
	})

	t.Run("wsl to windows to wsl for non-mnt paths with distro set", func(t *testing.T) {
		t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
		p := "/home/me/proj/a.ts"
		win, err := WSLToWindows(p)
		if err != nil {
			t.Fatalf("WSLToWindows(%q): %v", p, err)
		}
		back, err := WindowsToWSL(win)
		if err != nil {
			t.Fatalf("WindowsToWSL(%q): %v", win, err)
		}
		if back != p {
			t.Errorf("round trip %q -> %q -> %q, want %q", p, win, back, p)
		}
	})
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"drive backslash", `C:\Users\me\proj\a.ts`, `/mnt/c/Users/me/proj/a.ts`},
		{"drive forward slash", `C:/Users/me/proj/a.ts`, `/mnt/c/Users/me/proj/a.ts`},
		{"unc wsl$", `\\wsl$\Ubuntu\home\me\a.ts`, `/home/me/a.ts`},
		{"already posix", `/home/me/proj/a.ts`, `/home/me/proj/a.ts`},
		{"posix needs clean", `/home/me//proj/./a.ts`, `/home/me/proj/a.ts`},
		{"unsupported unc falls back syntactically", `\\server\share\file.ts`, `/server/share/file.ts`},
		{"empty", ``, ``},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Normalize(c.in)
			if got != c.want {
				t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestNormalizeIdempotent(t *testing.T) {
	inputs := []string{
		`C:\Users\me\proj\a.ts`,
		`C:/Users/me/proj/a.ts`,
		`\\wsl$\Ubuntu\home\me\a.ts`,
		`\\wsl.localhost\Ubuntu\home\me\a.ts`,
		`/home/me/proj/a.ts`,
		`/mnt/c/Users/me/a.ts`,
		`\\server\share\file.ts`,
		``,
	}
	for _, in := range inputs {
		once := Normalize(in)
		twice := Normalize(once)
		if once != twice {
			t.Errorf("Normalize not idempotent for %q: Normalize(x)=%q, Normalize(Normalize(x))=%q", in, once, twice)
		}
	}
}

func TestPathToURIAndBack(t *testing.T) {
	cases := []struct {
		name string
		p    string
		want string
	}{
		{"simple mnt path", `/mnt/c/Users/me/proj/a.ts`, `file:///mnt/c/Users/me/proj/a.ts`},
		{"posix home path", `/home/me/a.ts`, `file:///home/me/a.ts`},
		{"path with space", `/home/me/my proj/a.ts`, `file:///home/me/my%20proj/a.ts`},
		{"windows path input normalized first", `C:\Users\me\a.ts`, `file:///mnt/c/Users/me/a.ts`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PathToURI(c.p)
			if got != c.want {
				t.Errorf("PathToURI(%q) = %q, want %q", c.p, got, c.want)
			}
		})
	}
}

func TestURIToPath(t *testing.T) {
	cases := []struct {
		name    string
		uri     string
		want    string
		wantErr bool
	}{
		{"simple mnt uri", `file:///mnt/c/Users/me/proj/a.ts`, `/mnt/c/Users/me/proj/a.ts`, false},
		{"space encoded", `file:///home/me/my%20proj/a.ts`, `/home/me/my proj/a.ts`, false},
		{"localhost authority", `file://localhost/home/me/a.ts`, `/home/me/a.ts`, false},
		{"windows drive uri", `file:///C:/Users/me/a.ts`, `/mnt/c/Users/me/a.ts`, false},
		{"not a file uri", `https://example.com/a.ts`, ``, true},
		{"invalid uri", "file://%zz", ``, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := URIToPath(c.uri)
			if c.wantErr {
				if err == nil {
					t.Fatalf("URIToPath(%q) = %q, nil; want error", c.uri, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("URIToPath(%q) unexpected error: %v", c.uri, err)
			}
			if got != c.want {
				t.Errorf("URIToPath(%q) = %q, want %q", c.uri, got, c.want)
			}
		})
	}
}

// TestURIRoundTripUnicodeAndSpaces exercises percent-encoding round trips
// through PathToURI/URIToPath for paths containing spaces and unicode
// characters, which is the primary reason these helpers exist rather than
// simple string concatenation.
func TestURIRoundTripUnicodeAndSpaces(t *testing.T) {
	paths := []string{
		`/home/me/my proj/a.ts`,
		`/mnt/c/Users/me/déjà vu/文件.ts`,
		`/home/me/emoji-🚀-dir/a.ts`,
		`/home/me/a+b&c=d.ts`,
	}
	for _, p := range paths {
		uri := PathToURI(p)
		back, err := URIToPath(uri)
		if err != nil {
			t.Fatalf("URIToPath(%q): %v", uri, err)
		}
		if back != p {
			t.Errorf("URI round trip %q -> %q -> %q, want %q", p, uri, back, p)
		}
	}
}

func TestURIToPathUNCAuthority(t *testing.T) {
	got, err := URIToPath(`file://myserver/share/file.ts`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `//myserver/share/file.ts`
	if got != want {
		t.Errorf("URIToPath UNC authority = %q, want %q", got, want)
	}
}
