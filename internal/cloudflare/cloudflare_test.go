package cloudflare

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/richardamare/skybox/internal/sandbox"
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

func TestPreviewHostnameNeverNamesAnotherSandbox(t *testing.T) {
	c := New(Credentials{Zone: "example.com"})
	got := PreviewHostname(c.Hostname("api-fix"), 3000)
	if got != "3000-api-fix-sbx.example.com" {
		t.Fatalf("PreviewHostname = %q", got)
	}
	if err := sandbox.ValidateName("3000-api-fix"); err == nil {
		t.Fatal("3000-api-fix is a valid sandbox name, so a preview hostname can be another sandbox's SSH hostname")
	}
}

func TestRouteSendsEachHostnameToItsOwnPortOnTheSandbox(t *testing.T) {
	c, calls := fakeAPI(t, map[string]func() (int, any){
		"PUT /accounts/acc/cfd_tunnel/t1/configurations": ok(map[string]any{}),
	})
	if err := c.Route(t.Context(), "t1", "api-fix-sbx.example.com"); err != nil {
		t.Fatal(err)
	}
	routes := map[string]string{}
	var catchAll []string
	for _, rule := range (*calls)[0].body["config"].(map[string]any)["ingress"].([]any) {
		rule := rule.(map[string]any)
		host, _ := rule["hostname"].(string)
		if host == "" {
			catchAll = append(catchAll, rule["service"].(string))
			continue
		}
		routes[host] = rule["service"].(string)
	}
	want := map[string]string{"api-fix-sbx.example.com": "ssh://localhost:22"}
	for _, port := range PreviewPorts {
		want[fmt.Sprintf("%d-api-fix-sbx.example.com", port)] = fmt.Sprintf("http://localhost:%d", port)
	}
	if !maps.Equal(routes, want) {
		t.Fatalf("routes = %v; want %v", routes, want)
	}
	if !slices.Equal(catchAll, []string{"http_status:404"}) {
		t.Fatalf("catch-all = %v; want one rule answering 404", catchAll)
	}
}

func TestCreateDNSRecordsPointsEveryHostnameAtTheTunnelInOneBatch(t *testing.T) {
	c, calls := fakeAPI(t, map[string]func() (int, any){
		"POST /zones/zone/dns_records/batch": ok(map[string]any{}),
	})
	if err := c.CreateDNSRecords(t.Context(), "api-fix", "api-fix-sbx.example.com", "t1"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %v; want one batch", *calls)
	}
	var names []string
	for _, post := range (*calls)[0].body["posts"].([]any) {
		post := post.(map[string]any)
		if post["type"] != "CNAME" || post["content"] != "t1.cfargotunnel.com" || post["proxied"] != true || post["comment"] != "skybox:sandbox=api-fix" {
			t.Errorf("record = %v", post)
		}
		names = append(names, post["name"].(string))
	}
	if len(names) != 1+len(PreviewPorts) || names[0] != "api-fix-sbx.example.com" || names[1] != "3000-api-fix-sbx.example.com" {
		t.Fatalf("names = %v; want the SSH hostname and one per preview port", names)
	}
}

func TestCreateDNSRecordsNamesTheMissingPermission(t *testing.T) {
	c, _ := fakeAPI(t, map[string]func() (int, any){
		"POST /zones/zone/dns_records/batch": func() (int, any) { return http.StatusForbidden, nil },
	})
	err := c.CreateDNSRecords(t.Context(), "api-fix", "api-fix-sbx.example.com", "t1")

	var perm *PermissionError
	if !errors.As(err, &perm) || !strings.Contains(err.Error(), "Zone › DNS › Edit") {
		t.Fatalf("CreateDNSRecords = %v; want a PermissionError naming Zone › DNS › Edit", err)
	}
}

func TestDeleteDNSRecordsDeletesOnlyTheSandboxsRecords(t *testing.T) {
	var query url.Values
	c, calls := fakeAPI(t, map[string]func() (int, any){
		"GET /zones/zone/dns_records": func() (int, any) {
			return 200, []map[string]string{{"id": "r1"}, {"id": "r2"}}
		},
		"POST /zones/zone/dns_records/batch": ok(map[string]any{}),
	})
	c.http = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			query = r.URL.Query()
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	if err := c.DeleteDNSRecords(t.Context(), "api-fix"); err != nil {
		t.Fatal(err)
	}
	if query.Get("comment.exact") != "skybox:sandbox=api-fix" {
		t.Fatalf("query = %v; want the records whose comment is exactly skybox:sandbox=api-fix", query)
	}
	deletes := (*calls)[1].body["deletes"].([]any)
	if len(deletes) != 2 || deletes[0].(map[string]any)["id"] != "r1" || deletes[1].(map[string]any)["id"] != "r2" {
		t.Fatalf("deletes = %v; want r1 and r2", deletes)
	}
}

func TestDeleteDNSRecordsWithNoRecordsDeletesNothing(t *testing.T) {
	c, calls := fakeAPI(t, map[string]func() (int, any){
		"GET /zones/zone/dns_records": ok([]any{}),
	})
	if err := c.DeleteDNSRecords(t.Context(), "api-fix"); err != nil || len(*calls) != 1 {
		t.Fatalf("DeleteDNSRecords = %v after %v; want nil after the one list call", err, *calls)
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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
}
