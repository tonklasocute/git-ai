package diff

import (
	"fmt"
	"strings"
)

// DefaultExcludes are never sent to a model: they are noisy, huge, or not
// text. Users can re-include one with a "!pattern" in .git-quill-ignore.
var DefaultExcludes = []string{
	// lockfiles
	"package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml",
	"bun.lockb", "bun.lock", "go.sum", "Cargo.lock", "poetry.lock", "Pipfile.lock",
	"uv.lock", "composer.lock", "Gemfile.lock", "mix.lock", "pubspec.lock",
	"Podfile.lock", "flake.lock", "packages.lock.json", "gradle.lockfile", "*.lock",
	// dependency and build output directories
	"vendor/", "node_modules/", ".yarn/", "bower_components/", "Pods/",
	// minified, bundled and generated
	"*.min.js", "*.min.css", "*.min.mjs", "*.map", "*.bundle.js",
	"*.pb.go", "*_pb2.py", "*_pb2_grpc.py", "*.pb.cc", "*.pb.h",
	"*.generated.*", "*_generated.go", "*.g.dart", "*.freezed.dart",
	// images, media, fonts, archives, binaries, databases
	"*.png", "*.jpg", "*.jpeg", "*.gif", "*.webp", "*.avif", "*.bmp", "*.ico",
	"*.tif", "*.tiff", "*.svg", "*.psd", "*.heic",
	"*.mp3", "*.mp4", "*.mov", "*.avi", "*.wav", "*.flac", "*.ogg", "*.webm",
	"*.woff", "*.woff2", "*.ttf", "*.otf", "*.eot",
	"*.zip", "*.tar", "*.gz", "*.tgz", "*.bz2", "*.xz", "*.7z", "*.rar", "*.zst",
	"*.jar", "*.war", "*.exe", "*.dll", "*.so", "*.dylib", "*.a", "*.o", "*.obj",
	"*.class", "*.wasm", "*.pyc", "*.bin", "*.dat",
	"*.pdf", "*.sqlite", "*.sqlite3", "*.db",
}

// Excluded describes a file left out of the prompt and why.
type Excluded struct {
	Path   string
	Reason string
}

func (e Excluded) String() string { return fmt.Sprintf("%s (%s)", e.Path, e.Reason) }

// maxLineLen above which a file is treated as minified/generated.
const maxLineLen = 5000

// Filter splits files into those to send and those to leave out.
func Filter(files []File, m *Matcher) (kept []File, excluded []Excluded) {
	for _, f := range files {
		if reason := excludeReason(&f, m); reason != "" {
			excluded = append(excluded, Excluded{Path: f.Path(), Reason: reason})
			continue
		}
		kept = append(kept, f)
	}
	return kept, excluded
}

func excludeReason(f *File, m *Matcher) string {
	if m != nil && m.Match(f.Path()) {
		return "ignore pattern"
	}
	if f.Binary {
		return "binary"
	}
	for i, h := range f.Hunks {
		for j, l := range h.Lines {
			if len(l) > maxLineLen {
				return "minified (very long lines)"
			}
			// Generated-file markers live in the first few lines.
			if i == 0 && j < 10 && strings.HasPrefix(l, "+") && isGeneratedMarker(l) {
				return "generated"
			}
		}
	}
	return ""
}

func isGeneratedMarker(l string) bool {
	return (strings.Contains(l, "Code generated") && strings.Contains(l, "DO NOT EDIT")) ||
		strings.Contains(l, "@generated")
}
