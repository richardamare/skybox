package cloudflare

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type call struct {
	method, path string
	body         map[string]any
}

// fakeAPI answers each "METHOD /path" with a status and a result, and records every call.
func fakeAPI(t *testing.T, answers map[string]func() (int, any)) (*Client, *[]call) {
	t.Helper()
	var calls []call
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls = append(calls, call{r.Method, r.URL.Path, body})
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("%s %s sent no bearer token", r.Method, r.URL.Path)
		}
		answer, ok := answers[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":1003,"message":"not found"}]}`))
			return
		}
		status, result := answer()
		w.WriteHeader(status)
		if status >= 300 {
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
	}))
	t.Cleanup(server.Close)
	c := New(Credentials{Token: "token", AccountID: "acc", ZoneID: "zone", Zone: "example.com"})
	c.base = server.URL
	return c, &calls
}

func ok(result any) func() (int, any) { return func() (int, any) { return 200, result } }

func TestHostnameSitsOneLevelBelowTheZone(t *testing.T) {
	c := New(Credentials{Zone: "example.com"})
	if got := c.Hostname("api-fix"); got != "api-fix-sbx.example.com" {
		t.Fatalf("Hostname = %q", got)
	}
}

func TestCreateTunnelReturnsItsToken(t *testing.T) {
	c, calls := fakeAPI(t, map[string]func() (int, any){
		"POST /accounts/acc/cfd_tunnel":         ok(map[string]string{"id": "t1"}),
		"GET /accounts/acc/cfd_tunnel/t1/token": ok("secret-token"),
	})
	tunnel, err := c.CreateTunnel(t.Context(), "skybox-api-fix")

	if err != nil || tunnel.ID != "t1" || tunnel.Token != "secret-token" {
		t.Fatalf("CreateTunnel = %+v, %v", tunnel, err)
	}
	if (*calls)[0].body["config_src"] != "cloudflare" {
		t.Fatal("the tunnel is not configured from Cloudflare")
	}
}

func TestCreateTunnelNamesTheMissingPermission(t *testing.T) {
	c, _ := fakeAPI(t, map[string]func() (int, any){
		"POST /accounts/acc/cfd_tunnel": func() (int, any) { return http.StatusForbidden, nil },
	})
	_, err := c.CreateTunnel(t.Context(), "skybox-api-fix")

	var perm *PermissionError
	if !errors.As(err, &perm) || !strings.Contains(err.Error(), "Cloudflare Tunnel › Edit") {
		t.Fatalf("CreateTunnel = %v; want a PermissionError naming Cloudflare Tunnel › Edit", err)
	}
}

func TestRouteSSHSendsOnlyTheHostnameToSSHD(t *testing.T) {
	c, calls := fakeAPI(t, map[string]func() (int, any){
		"PUT /accounts/acc/cfd_tunnel/t1/configurations": ok(map[string]any{}),
	})
	if err := c.RouteSSH(t.Context(), "t1", "api-fix-sbx.example.com"); err != nil {
		t.Fatal(err)
	}
	ingress := (*calls)[0].body["config"].(map[string]any)["ingress"].([]any)
	first, last := ingress[0].(map[string]any), ingress[len(ingress)-1].(map[string]any)
	if first["hostname"] != "api-fix-sbx.example.com" || first["service"] != "ssh://localhost:22" || last["service"] != "http_status:404" {
		t.Fatalf("ingress = %v", ingress)
	}
}

func TestCreateDNSRecordPointsAtTheTunnelAndNamesTheSandbox(t *testing.T) {
	c, calls := fakeAPI(t, map[string]func() (int, any){
		"POST /zones/zone/dns_records": ok(map[string]string{"id": "r1"}),
	})
	id, err := c.CreateDNSRecord(t.Context(), "api-fix", "api-fix-sbx.example.com", "t1")

	body := (*calls)[0].body
	if err != nil || id != "r1" || body["content"] != "t1.cfargotunnel.com" || body["proxied"] != true || body["comment"] != "skybox:sandbox=api-fix" {
		t.Fatalf("CreateDNSRecord = %q, %v, body %v", id, err, body)
	}
}

func TestTunnelHealthy(t *testing.T) {
	status := "inactive"
	c, _ := fakeAPI(t, map[string]func() (int, any){
		"GET /accounts/acc/cfd_tunnel/t1": func() (int, any) { return 200, map[string]string{"status": status} },
	})
	if healthy, err := c.TunnelHealthy(t.Context(), "t1"); err != nil || healthy {
		t.Fatalf("inactive tunnel: healthy = %v, %v", healthy, err)
	}
	status = "healthy"
	if healthy, err := c.TunnelHealthy(t.Context(), "t1"); err != nil || !healthy {
		t.Fatalf("healthy tunnel: healthy = %v, %v", healthy, err)
	}
}

func TestDeleteTunnelDropsConnectionsFirstAndIgnoresAGoneTunnel(t *testing.T) {
	c, calls := fakeAPI(t, map[string]func() (int, any){
		"DELETE /accounts/acc/cfd_tunnel/t1/connections": ok(nil),
		"DELETE /accounts/acc/cfd_tunnel/t1":             ok(nil),
	})
	if err := c.DeleteTunnel(t.Context(), "t1"); err != nil {
		t.Fatal(err)
	}
	if (*calls)[0].path != "/accounts/acc/cfd_tunnel/t1/connections" || (*calls)[1].path != "/accounts/acc/cfd_tunnel/t1" {
		t.Fatalf("calls = %v", *calls)
	}
	if err := c.DeleteTunnel(t.Context(), "gone"); err != nil {
		t.Fatalf("DeleteTunnel(gone) = %v; want nil", err)
	}
	if err := c.DeleteDNSRecord(t.Context(), "gone"); err != nil {
		t.Fatalf("DeleteDNSRecord(gone) = %v; want nil", err)
	}
}
