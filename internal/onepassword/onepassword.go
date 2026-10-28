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
	"github.com/richardamare/skybox/internal/tailscale"
)

const (
	vault          = "skybox"
	cloudflareItem = "Cloudflare API token"
	tailscaleItem  = "Tailscale OAuth client"
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
	itemJSON, err := getItem(ctx, cloudflareItem)
	if err != nil {
		return cloudflare.Credentials{}, err
	}
	return parseCloudflare(itemJSON)
}

// Tailscale reads the OAuth client that mints sandboxes' auth keys and deletes their devices.
func Tailscale(ctx context.Context) (tailscale.Credentials, error) {
	itemJSON, err := getItem(ctx, tailscaleItem)
	if err != nil {
		return tailscale.Credentials{}, err
	}
	return parseTailscale(itemJSON)
}

func getItem(ctx context.Context, item string) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "op", "item", "get", item, "--vault", vault, "--format", "json")
	cmd.Env = opEnv(os.Environ(), ServiceAccountTokenFile())
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, &ReadError{Item: item, Detail: detail}
	}
	return out, nil
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
	v, err := fields(cloudflareItem, itemJSON,
		field{"", "credential"}, field{"Cloudflare", "account id"}, field{"Cloudflare", "zone id"}, field{"Cloudflare", "zone"})
	if err != nil {
		return cloudflare.Credentials{}, err
	}
	return cloudflare.Credentials{Token: v[0], AccountID: v[1], ZoneID: v[2], Zone: v[3]}, nil
}

// parseTailscale reads an API Credential item, whose username is the OAuth client's ID and whose credential is its
// secret.
func parseTailscale(itemJSON []byte) (tailscale.Credentials, error) {
	v, err := fields(tailscaleItem, itemJSON, field{"", "username"}, field{"", "credential"})
	if err != nil {
		return tailscale.Credentials{}, err
	}
	return tailscale.Credentials{ClientID: v[0], ClientSecret: v[1]}, nil
}

// field names a field by its section and label, as the 1Password app shows them. An empty section is the top of the
// item.
type field struct{ section, label string }

func (f field) String() string {
	if f.section == "" {
		return f.label
	}
	return f.section + "/" + f.label
}

// fields returns the values of the wanted fields, in their order, from op's JSON for item. It fails naming the first
// field that is empty or missing.
func fields(item string, itemJSON []byte, wanted ...field) ([]string, error) {
	var parsed struct {
		Fields []struct {
			Label   string `json:"label"`
			Value   string `json:"value"`
			Section *struct {
				Label string `json:"label"`
			} `json:"section"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(itemJSON, &parsed); err != nil {
		return nil, &ReadError{Item: item, Detail: "op printed something that is not an item"}
	}
	values := make([]string, len(wanted))
	for i, w := range wanted {
		for _, f := range parsed.Fields {
			section := ""
			if f.Section != nil {
				section = f.Section.Label
			}
			if f.Label == w.label && section == w.section {
				values[i] = f.Value
				break
			}
		}
		if values[i] == "" {
			return nil, &ReadError{Item: item, Detail: "the field " + w.String() + " is empty or missing"}
		}
	}
	return values, nil
}
