package marginfuse

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The user-agent must carry the version this module was released as, because
// it is the first thing anyone asks when a request behaves oddly.
func TestUserAgentReportsTheModuleVersion(t *testing.T) {
	want := "marginfuse-go/" + Version
	if userAgent != want {
		t.Fatalf("user-agent is %q, want %q", userAgent, want)
	}
}

// Version has to look like a version, since the release workflow compares it
// against a tag by string equality.
func TestVersionIsSemver(t *testing.T) {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(Version) {
		t.Fatalf("Version %q is not major.minor.patch", Version)
	}
}

// The changelog is what a user reads to decide whether to upgrade, so it has
// to have an entry for the version actually being shipped.
func TestChangelogDocumentsThisVersion(t *testing.T) {
	body, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatalf("reading changelog: %v", err)
	}
	if !strings.Contains(string(body), "["+Version+"]") {
		t.Fatalf("CHANGELOG.md has no [%s] section", Version)
	}
}

// The tag being released has to name the version the code reports.
//
// The release workflow sets MARGINFUSE_RELEASE_TAG; outside a release it is
// unset and this test skips. Keeping the comparison here rather than in shell
// means it reads the same constant the SDK does, instead of a copy scraped out
// of the source.
func TestReleaseTagMatchesVersion(t *testing.T) {
	tag, ok := os.LookupEnv("MARGINFUSE_RELEASE_TAG")
	if !ok {
		t.Skip("not a release build")
	}
	if want := "v" + Version; tag != want {
		t.Fatalf("releasing tag %s but the SDK reports %s. The module proxy caches "+
			"a version permanently and it cannot be replaced, so correct version.go "+
			"and release the next patch version instead of reusing this tag.", tag, want)
	}
}
