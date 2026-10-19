package app

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// ChangelogHeader starts a new CHANGELOG.md.
const ChangelogHeader = `# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
`

var sectionRe = regexp.MustCompile(`(?m)^## `)

// SectionHeading formats "## [v1.2.0] - 2026-10-19" or "## [Unreleased]".
func SectionHeading(version, date string) string {
	if version == "" || strings.EqualFold(version, "unreleased") {
		return "## [Unreleased]"
	}
	return fmt.Sprintf("## [%s] - %s", version, date)
}

// PrependSection inserts section (which starts with "## ") into the
// changelog at path, creating the file if needed. An existing
// [Unreleased] section is replaced when writing Unreleased; writing a
// version that already has a section is an error.
func PrependSection(path, section string) error {
	section = strings.TrimSpace(section) + "\n"
	heading := strings.SplitN(section, "\n", 2)[0]
	old, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.WriteFile(path, []byte(ChangelogHeader+"\n"+section), 0o644)
	}
	if err != nil {
		return err
	}
	content := string(old)
	key := strings.SplitN(heading, " - ", 2)[0] // "## [v1.2.0]"

	locs := sectionRe.FindAllStringIndex(content, -1)
	for i, loc := range locs {
		lineEnd := strings.IndexByte(content[loc[0]:], '\n')
		line := content[loc[0]:]
		if lineEnd >= 0 {
			line = line[:lineEnd]
		}
		if strings.SplitN(line, " - ", 2)[0] != key {
			continue
		}
		if key != "## [Unreleased]" {
			return fmt.Errorf("%s already has a section for %s", path, strings.TrimPrefix(key, "## "))
		}
		end := len(content)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		content = content[:loc[0]] + section + "\n" + content[end:]
		return os.WriteFile(path, []byte(strings.TrimRight(content, "\n")+"\n"), 0o644)
	}

	// Insert before the first section, but keep an existing [Unreleased]
	// section on top when adding a version.
	at := len(content)
	if len(locs) > 0 {
		at = locs[0][0]
		if key != "## [Unreleased]" && strings.HasPrefix(content[at:], "## [Unreleased]") {
			at = len(content)
			if len(locs) > 1 {
				at = locs[1][0]
			}
		}
	}
	if at == len(content) {
		content = strings.TrimRight(content, "\n") + "\n\n" + section
	} else {
		content = content[:at] + section + "\n" + content[at:]
	}
	return os.WriteFile(path, []byte(strings.TrimRight(content, "\n")+"\n"), 0o644)
}
