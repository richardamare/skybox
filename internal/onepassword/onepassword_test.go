package onepassword

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const cloudflareItemJSON = `{"fields":[
  {"label":"credential","value":"token"},
  {"label":"account id","value":"account","section":{"label":"Cloudflare"}},
  {"label":"zone id","value":"zone-id","section":{"label":"Cloudflare"}},
  {"label":"zone","value":"example.com","section":{"label":"Cloudflare"}}
]}`

func TestParseCloudflareReadsEveryField(t *testing.T) {
	creds, err := parseCloudflare([]byte(cloudflareItemJSON))
	if err != nil || creds.Token != "token" || creds.AccountID != "account" || creds.ZoneID != "zone-id" || creds.Zone != "example.com" {
		t.Fatalf("parseCloudflare = %+v, %v", creds, err)
	}
}

func TestParseCloudflareNamesTheItemWhenAFieldIsMissing(t *testing.T) {
	_, err := parseCloudflare([]byte(strings.Replace(cloudflareItemJSON, `"zone","value":"example.com"`, `"zone","value":""`, 1)))

	var readErr *ReadError
	if !errors.As(err, &readErr) || !strings.Contains(err.Error(), `"Cloudflare API token"`) || !strings.Contains(err.Error(), "Cloudflare/zone") {
		t.Fatalf("parseCloudflare = %v; want an error naming the item and the field", err)
	}
}

const tailscaleItemJSON = `{"fields":[
  {"id":"username","label":"username","value":"client-id"},
  {"id":"credential","label":"credential","value":"tskey-client-secret"},
  {"id":"notesPlain","label":"notesPlain","value":"tailnet richardamare.github"}
]}`

func TestParseTailscaleReadsTheClientIDAndSecret(t *testing.T) {
	creds, err := parseTailscale([]byte(tailscaleItemJSON))
	if err != nil || creds.ClientID != "client-id" || creds.ClientSecret != "tskey-client-secret" {
		t.Fatalf("parseTailscale = %+v, %v", creds, err)
	}
}

func TestParseTailscaleNamesTheItemWhenTheSecretIsMissing(t *testing.T) {
	_, err := parseTailscale([]byte(strings.Replace(tailscaleItemJSON, `"tskey-client-secret"`, `""`, 1)))

	var readErr *ReadError
	if !errors.As(err, &readErr) || !strings.Contains(err.Error(), `"Tailscale OAuth client"`) || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("parseTailscale = %v; want an error naming the item and the field", err)
	}
}

func TestOpEnvAddsTheServiceAccountToken(t *testing.T) {
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("ops_test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := opEnv([]string{"HOME=/home/me"}, file)
	if !slices.Contains(env, "OP_SERVICE_ACCOUNT_TOKEN=ops_test") {
		t.Fatalf("opEnv = %v; want the service account token", env)
	}
}

func TestOpEnvKeepsAnAccountTheEnvironmentNames(t *testing.T) {
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("ops_file"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := opEnv([]string{"OP_SERVICE_ACCOUNT_TOKEN=ops_env"}, file)
	if len(env) != 1 || env[0] != "OP_SERVICE_ACCOUNT_TOKEN=ops_env" {
		t.Fatalf("opEnv = %v; want the environment's token alone", env)
	}
}

func TestOpEnvWithoutATokenFileLeavesOpToTheDesktopApp(t *testing.T) {
	env := opEnv([]string{"HOME=/home/me"}, filepath.Join(t.TempDir(), "missing"))
	if len(env) != 1 {
		t.Fatalf("opEnv = %v; want the environment unchanged", env)
	}
}
