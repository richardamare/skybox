package skybox

import "testing"

func TestParseVersionReadsVersionField(t *testing.T) {
	got := parseVersion([]byte(`{"name":"skybox","version":"1.2.3"}`))
	if got != "1.2.3" {
		t.Fatalf("parseVersion() = %q, want %q", got, "1.2.3")
	}
}

func TestParseVersionWithoutVersionFieldIsEmpty(t *testing.T) {
	if got := parseVersion([]byte(`{"name":"skybox"}`)); got != "" {
		t.Fatalf("parseVersion() = %q, want empty", got)
	}
}
