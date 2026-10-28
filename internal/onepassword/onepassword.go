// Package onepassword reads skybox's keys from the skybox 1Password vault with the op CLI, at the moment they are
// needed, so no key is ever written to disk.
package onepassword

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/richardamare/skybox/internal/cloudflare"
)

const (
	vault          = "skybox"
	cloudflareItem = "Cloudflare API token"
)

// ReadError names the 1Password item op could not read.
type ReadError struct {
	Item   string
	Detail string
}

func (e *ReadError) Error() string {
	return fmt.Sprintf("op cannot read the 1Password item %q in the %s vault: %s", e.Item, vault, e.Detail)
}

// ServiceAccountTokenFile holds the token of the read-only service account that agents on the Mac use, since they
// cannot answer op's biometric prompt.
func ServiceAccountTokenFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "skybox", "op-service-account-token")
}

// Cloudflare reads the API token and the account and zone it works on, all from one item.
func Cloudflare(ctx context.Context) (cloudflare.Credentials, error) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "op", "item", "get", cloudflareItem, "--vault", vault, "--format", "json")
	cmd.Env = opEnv(os.Environ(), ServiceAccountTokenFile())
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return cloudflare.Credentials{}, &ReadError{Item: cloudflareItem, Detail: detail}
	}
	return parseCloudflare(out)
}

// opEnv passes op the service account token from tokenFile, unless the environment already names an account.
func opEnv(environ []string, tokenFile string) []string {
	for _, kv := range environ {
		if strings.HasPrefix(kv, "OP_SERVICE_ACCOUNT_TOKEN=") {
			return environ
		}
	}
	token, err := os.ReadFile(tokenFile)
	if err != nil || len(bytes.TrimSpace(token)) == 0 {
		return environ
	}
	return append(environ, "OP_SERVICE_ACCOUNT_TOKEN="+string(bytes.TrimSpace(token)))
}

func parseCloudflare(itemJSON []byte) (cloudflare.Credentials, error) {
	var item struct {
		Fields []struct {
			ID      string `json:"id"`
			Label   string `json:"label"`
			Value   string `json:"value"`
			Section *struct {
				Label string `json:"label"`
			} `json:"section"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(itemJSON, &item); err != nil {
		return cloudflare.Credentials{}, &ReadError{Item: cloudflareItem, Detail: "op printed something that is not an item"}
	}
	field := func(section, label string) string {
		for _, f := range item.Fields {
			s := ""
			if f.Section != nil {
				s = f.Section.Label
			}
			if f.Label == label && s == section {
				return f.Value
			}
		}
		return ""
	}
	creds := cloudflare.Credentials{
		Token:     field("", "credential"),
		AccountID: field("Cloudflare", "account id"),
		ZoneID:    field("Cloudflare", "zone id"),
		Zone:      field("Cloudflare", "zone"),
	}
	for name, value := range map[string]string{
		"credential": creds.Token, "Cloudflare/account id": creds.AccountID,
		"Cloudflare/zone id": creds.ZoneID, "Cloudflare/zone": creds.Zone,
	} {
		if value == "" {
			return cloudflare.Credentials{}, &ReadError{Item: cloudflareItem, Detail: "the field " + name + " is empty or missing"}
		}
	}
	return creds, nil
}
