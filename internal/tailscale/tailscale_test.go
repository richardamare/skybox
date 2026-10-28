package tailscale

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

const secret = "tskey-client-kXYZ-secret"

type call struct {
	method, path, auth string
	body               map[string]any
	form               map[string][]string
}

// fakeAPI answers the token endpoint itself, then each "METHOD /path" with a status and a JSON body, and records
// every call.
func fakeAPI(t *testing.T, answers map[string]func() (int, any)) (*Client, *[]call) {
	t.Helper()
	var calls []call
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := call{method: r.Method, path: r.URL.Path, auth: r.Header.Get("Authorization")}
		if r.URL.Path == "/api/v2/oauth/token" {
			_ = r.ParseForm()
			c.form = r.PostForm
		} else {
			_ = json.NewDecoder(r.Body).Decode(&c.body)
		}
		calls = append(calls, c)
		answer, ok := answers[r.Method+" "+r.URL.Path]
		if !ok && r.URL.Path == "/api/v2/oauth/token" {
			answer, ok = func() (int, any) { return 200, map[string]any{"access_token": "access", "token_type": "Bearer"} }, true
		}
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "not found"})
			return
		}
		status, body := answer()
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(server.Close)
	c := New(Credentials{ClientID: "client-id", ClientSecret: secret})
	c.base = server.URL + "/api/v2"
	return c, &calls
}

func ok(body any) func() (int, any) { return func() (int, any) { return 200, body } }

func TestTheSecretGoesOnlyToTheTokenEndpoint(t *testing.T) {
	c, calls := fakeAPI(t, map[string]func() (int, any){
		"POST /api/v2/tailnet/-/keys":   ok(map[string]string{"id": "k1", "key": "tskey-auth-k1-abc"}),
		"GET /api/v2/tailnet/-/devices": ok(map[string]any{"devices": []any{}}),
	})
	if _, err := c.MintAuthKey(t.Context(), "api-fix"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Devices(t.Context(), "api-fix"); err != nil {
		t.Fatal(err)
	}

	if len(*calls) != 3 {
		t.Fatalf("calls = %+v; want one token request, then the two calls with its token", *calls)
	}
	token := (*calls)[0]
	if token.method != "POST" || token.form["client_secret"][0] != secret || token.form["client_id"][0] != "client-id" ||
		token.form["grant_type"][0] != "client_credentials" {
		t.Fatalf("token request = %+v; want the client credentials as a form", token)
	}
	for _, c := range (*calls)[1:] {
		if c.auth != "Bearer access" || strings.Contains(c.auth, secret) {
			t.Errorf("%s %s sent %q; want the access token alone", c.method, c.path, c.auth)
		}
	}
}

func TestMintAuthKeyAsksForASingleUsePreapprovedTaggedKeyThatExpiresInMinutes(t *testing.T) {
	c, calls := fakeAPI(t, map[string]func() (int, any){
		"POST /api/v2/tailnet/-/keys": ok(map[string]string{"id": "k1", "key": "tskey-auth-k1-abc"}),
	})
	key, err := c.MintAuthKey(t.Context(), "api-fix")

	if err != nil || key.ID != "k1" || key.Key != "tskey-auth-k1-abc" {
		t.Fatalf("MintAuthKey = %+v, %v", key, err)
	}
	body := (*calls)[1].body
	create := body["capabilities"].(map[string]any)["devices"].(map[string]any)["create"].(map[string]any)
	if create["reusable"] != false || create["ephemeral"] != false || create["preauthorized"] != true {
		t.Errorf("key capabilities = %v; want single use, not ephemeral, pre-approved", create)
	}
	if tags, _ := create["tags"].([]any); len(tags) != 1 || tags[0] != Tag {
		t.Errorf("key tags = %v; want %s alone", create["tags"], Tag)
	}
	if expiry, _ := body["expirySeconds"].(float64); expiry <= 0 || expiry > 3600 {
		t.Errorf("expirySeconds = %v; want minutes, not hours", body["expirySeconds"])
	}
}

func TestMintAuthKeyNamesTheMissingScope(t *testing.T) {
	c, _ := fakeAPI(t, map[string]func() (int, any){
		"POST /api/v2/tailnet/-/keys": func() (int, any) {
			return http.StatusForbidden, map[string]string{"message": "calling actor does not have enough permissions"}
		},
	})
	_, err := c.MintAuthKey(t.Context(), "api-fix")

	var scope *ScopeError
	if !errors.As(err, &scope) || !strings.Contains(err.Error(), "auth_keys") || !strings.Contains(err.Error(), Tag) {
		t.Fatalf("MintAuthKey = %v; want a ScopeError naming auth_keys and %s", err, Tag)
	}
}

func TestARefusedClientNeverRepeatsItsSecret(t *testing.T) {
	c, calls := fakeAPI(t, map[string]func() (int, any){
		"POST /api/v2/oauth/token": func() (int, any) {
			return http.StatusUnauthorized, map[string]string{"message": "invalid client credentials"}
		},
	})
	_, err := c.MintAuthKey(t.Context(), "api-fix")

	var refused *CredentialsError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "invalid client credentials") {
		t.Fatalf("MintAuthKey = %v; want a CredentialsError with Tailscale's reason", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "kXYZ") {
		t.Fatalf("the error %q repeats the client secret", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d; want none after the refused token request", len(*calls))
	}
}

func TestDevicesFindsTheSandboxByHostnameAndTag(t *testing.T) {
	c, _ := fakeAPI(t, map[string]func() (int, any){
		"GET /api/v2/tailnet/-/devices": ok(map[string]any{"devices": []map[string]any{
			{"nodeId": "n1", "hostname": "api-fix", "name": "api-fix.tail1234.ts.net.", "tags": []string{Tag}},
			{"nodeId": "n2", "hostname": "api-fix", "name": "api-fix-1.tail1234.ts.net", "tags": []string{}},
			{"nodeId": "n3", "hostname": "web", "name": "web.tail1234.ts.net", "tags": []string{Tag}},
		}}),
	})
	devices, err := c.Devices(t.Context(), "api-fix")

	if err != nil || len(devices) != 1 || devices[0].ID != "n1" || devices[0].Name != "api-fix.tail1234.ts.net" {
		t.Fatalf("Devices = %+v, %v; want the tagged api-fix alone, by its MagicDNS name", devices, err)
	}
}

func TestDevicesFindsTheSandboxByItsMagicDNSName(t *testing.T) {
	c, _ := fakeAPI(t, map[string]func() (int, any){
		"GET /api/v2/tailnet/-/devices": ok(map[string]any{"devices": []map[string]any{
			{"nodeId": "n1", "hostname": "vm-skybox-api-fix", "name": "api-fix.tail1234.ts.net", "tags": []string{Tag}},
			{"nodeId": "n2", "hostname": "vm-skybox-web", "name": "web.tail1234.ts.net", "tags": []string{Tag}},
		}}),
	})
	devices, err := c.Devices(t.Context(), "api-fix")

	if err != nil || len(devices) != 1 || devices[0].ID != "n1" {
		t.Fatalf("Devices = %+v, %v; want api-fix, found by its MagicDNS name", devices, err)
	}
}

func TestDeleteDevicesDeletesOnlyTheSandboxsDevices(t *testing.T) {
	c, calls := fakeAPI(t, map[string]func() (int, any){
		"GET /api/v2/tailnet/-/devices": ok(map[string]any{"devices": []map[string]any{
			{"nodeId": "n1", "hostname": "api-fix", "name": "api-fix.tail1234.ts.net", "tags": []string{Tag}},
			{"nodeId": "n2", "hostname": "api-fix", "name": "api-fix-1.tail1234.ts.net", "tags": []string{Tag}},
			{"nodeId": "n3", "hostname": "api-fix", "name": "macbook.tail1234.ts.net", "tags": []string{}},
			{"nodeId": "n4", "hostname": "web", "name": "web.tail1234.ts.net", "tags": []string{Tag}},
		}}),
		"DELETE /api/v2/device/n1": ok(nil),
	})
	if err := c.DeleteDevices(t.Context(), "api-fix"); err != nil {
		t.Fatal(err)
	}

	var deleted []string
	for _, c := range *calls {
		if c.method == http.MethodDelete {
			deleted = append(deleted, c.path)
		}
	}
	// n2 answers 404, as a device someone else already removed does.
	if !slices.Equal(deleted, []string{"/api/v2/device/n1", "/api/v2/device/n2"}) {
		t.Fatalf("deleted %v; want both tagged api-fix devices and nothing else", deleted)
	}
}

func TestDeleteDevicesNamesTheMissingScope(t *testing.T) {
	c, _ := fakeAPI(t, map[string]func() (int, any){
		"GET /api/v2/tailnet/-/devices": func() (int, any) { return http.StatusForbidden, map[string]string{"message": "forbidden"} },
	})
	err := c.DeleteDevices(t.Context(), "api-fix")

	var scope *ScopeError
	if !errors.As(err, &scope) || scope.Scope != "devices:core" {
		t.Fatalf("DeleteDevices = %v; want a ScopeError naming devices:core", err)
	}
}

func TestRevokeAuthKeyAcceptsAKeyAlreadyGone(t *testing.T) {
	c, calls := fakeAPI(t, nil)

	if err := c.RevokeAuthKey(t.Context(), "k1"); err != nil {
		t.Fatalf("RevokeAuthKey = %v; want a missing key to count as revoked", err)
	}
	if last := (*calls)[len(*calls)-1]; last.method != http.MethodDelete || last.path != "/api/v2/tailnet/-/keys/k1" {
		t.Fatalf("last call = %s %s", last.method, last.path)
	}
}
