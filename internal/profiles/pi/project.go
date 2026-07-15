// SPDX-License-Identifier: AGPL-3.0-only

package pi

import (
	"path/filepath"
	"strings"

	"github.com/openbox-dev/openbox/internal/domain"
)

// GlobalAgentDir is Pi's per-user agent directory (~/.pi/agent).
func GlobalAgentDir(home string) string {
	return filepath.Join(home, ".pi", "agent")
}

// ProjectPiDir is the project-local Pi directory (<project>/.pi).
func ProjectPiDir(projectRoot string) string {
	return filepath.Join(projectRoot, ".pi")
}

// MaterializePlan names the files OpenBox may write when applying a shared profile.
// TrustFile and ProjectSettings are intentionally empty — OpenBox never writes
// Pi trust decisions or project-local overlays.
type MaterializePlan struct {
	GlobalSettings  string
	TrustFile       string
	ProjectSettings string
}

// MaterializeTargets returns the only destinations OpenBox may write for a
// shared profile apply. projectRoot is accepted so callers can reason about
// separation; it is never used as a write target here.
func MaterializeTargets(home, projectRoot string) MaterializePlan {
	_ = projectRoot
	return MaterializePlan{
		GlobalSettings: filepath.Join(GlobalAgentDir(home), "settings.json"),
	}
}

// ValidateProjectResourcePath ensures rel stays inside projectRoot/.pi when
// resolved. Absolute paths, home shortcuts, and .. escapes are rejected.
func ValidateProjectResourcePath(projectRoot, rel string) error {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "path"}
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "~") {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "path"}
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "path"}
	}
	root := filepath.Clean(ProjectPiDir(projectRoot))
	joined := filepath.Clean(filepath.Join(root, clean))
	sep := string(filepath.Separator)
	if joined != root && !strings.HasPrefix(joined, root+sep) {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "path"}
	}
	return nil
}
