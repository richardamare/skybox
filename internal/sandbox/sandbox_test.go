package sandbox

import (
	"strings"
	"testing"
)

func TestParseSizeListsEveryProfileForAnUnknownOne(t *testing.T) {
	_, err := ParseSize("huge")
	if err == nil || !strings.Contains(err.Error(), "light, standard or large") {
		t.Fatalf("ParseSize(huge) = %v; want an error listing light, standard and large", err)
	}
}

func TestValidateName(t *testing.T) {
	for _, name := range []string{"api-fix", "a", "web2", "abcdefghijabcdefghijabcdefghij"} {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v", name, err)
		}
	}
	for _, name := range []string{"", "Api", "2fast", "api-", "-api", "api--fix", "api_fix", "abcdefghijabcdefghijabcdefghijk"} {
		if ValidateName(name) == nil {
			t.Errorf("ValidateName(%q) accepted an invalid name", name)
		}
	}
}
