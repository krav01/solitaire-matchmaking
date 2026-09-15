package releasepolicy

import (
	"os/exec"
	"testing"
)

func TestReleaseTagPolicy(t *testing.T) {
	t.Parallel()

	accepted := []string{
		"v0.9.0",
		"v0.9.0-rc.1",
		"v1.0.0",
		"v12.34.56-rc.7",
	}
	for _, tag := range accepted {
		tag := tag
		t.Run("accept_"+tag, func(t *testing.T) {
			t.Parallel()
			// #nosec G204 -- test-only fixed executable/script; tag is passed as a data argument, not shell-interpolated.
			if output, err := exec.Command("bash", "../../scripts/validate-release-tag.sh", tag).CombinedOutput(); err != nil {
				t.Fatalf("expected %q to be accepted: %v: %s", tag, err, output)
			}
		})
	}

	rejected := []string{
		"0.9.0",
		"v01.2.3",
		"v0.9",
		"v0.9.0-rc.0",
		"v0.9.0-rc",
		"v0.9.0-beta.1",
		"v0.9.0+build.1",
	}
	for _, tag := range rejected {
		tag := tag
		t.Run("reject_"+tag, func(t *testing.T) {
			t.Parallel()
			// #nosec G204 -- test-only fixed executable/script; tag is passed as a data argument, not shell-interpolated.
			if err := exec.Command("bash", "../../scripts/validate-release-tag.sh", tag).Run(); err == nil {
				t.Fatalf("expected %q to be rejected", tag)
			}
		})
	}
}
