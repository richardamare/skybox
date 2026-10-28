// Package cloudflare creates and deletes the tunnel and DNS record that make a sandbox reachable over SSH.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Credentials come from the Cloudflare API token item in the skybox 1Password vault.
type Credentials struct {
	Token     string
	AccountID string
	ZoneID    string
	Zone      string
}

type Client struct {
	creds Credentials
	base  string
	http  *http.Client
}

func New(creds Credentials) *Client {
	return &Client{creds: creds, base: "https://api.cloudflare.com/client/v4", http: http.DefaultClient}
}

// Hostname is the sandbox's SSH hostname. It sits one level below the zone, where the zone's Universal SSL
// certificate covers it, and ends in -sbx, so one Access application covers every sandbox.
func (c *Client) Hostname(sandbox string) string {
	return sandbox + "-sbx." + c.creds.Zone
}

// PermissionError reports a call the API token is not allowed to make, with the permission it lacks.
type PermissionError struct {
	Action     string
	Permission string
	Detail     string
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("the Cloudflare API token cannot %s: give it the permission %s (%s)", e.Action, e.Permission, e.Detail)
}

type Tunnel struct {
	ID    string
	Token string
}

func (c *Client) CreateTunnel(ctx context.Context, name string) (Tunnel, error) {
	var created struct {
		ID string `json:"id"`
	}
	err := c.call(ctx, http.MethodPost, c.account("/cfd_tunnel"), map[string]string{"name": name, "config_src": "cloudflare"}, &created)
	if err != nil {
		return Tunnel{}, withPermission(err, "create a tunnel", "Account › Cloudflare Tunnel › Edit")
	}
	var token string
	if err := c.call(ctx, http.MethodGet, c.account("/cfd_tunnel/"+created.ID+"/token"), nil, &token); err != nil {
		return Tunnel{ID: created.ID}, err
	}
	return Tunnel{ID: created.ID, Token: token}, nil
}

// RouteSSH sends the hostname to the sandbox's sshd and answers every other request with 404.
func (c *Client) RouteSSH(ctx context.Context, tunnelID, hostname string) error {
	config := map[string]any{"config": map[string]any{"ingress": []map[string]string{
		{"hostname": hostname, "service": "ssh://localhost:22"},
		{"service": "http_status:404"},
	}}}
	return c.call(ctx, http.MethodPut, c.account("/cfd_tunnel/"+tunnelID+"/configurations"), config, nil)
}

// CreateDNSRecord points hostname at the tunnel. The comment names the sandbox, so a record left behind can be
// traced to it.
func (c *Client) CreateDNSRecord(ctx context.Context, sandbox, hostname, tunnelID string) (string, error) {
	var record struct {
		ID string `json:"id"`
	}
	err := c.call(ctx, http.MethodPost, c.zone("/dns_records"), map[string]any{
		"type":    "CNAME",
		"name":    hostname,
		"content": tunnelID + ".cfargotunnel.com",
		"proxied": true,
		"comment": "skybox:sandbox=" + sandbox,
	}, &record)
	if err != nil {
		return "", withPermission(err, "create a DNS record", "Zone › DNS › Edit")
	}
	return record.ID, nil
}

// TunnelHealthy reports whether cloudflared on the sandbox holds connections to Cloudflare.
func (c *Client) TunnelHealthy(ctx context.Context, tunnelID string) (bool, error) {
	var tunnel struct {
		Status string `json:"status"`
	}
	if err := c.call(ctx, http.MethodGet, c.account("/cfd_tunnel/"+tunnelID), nil, &tunnel); err != nil {
		return false, err
	}
	return tunnel.Status == "healthy", nil
}

func (c *Client) DeleteDNSRecord(ctx context.Context, recordID string) error {
	return ignoreNotFound(c.call(ctx, http.MethodDelete, c.zone("/dns_records/"+recordID), nil, nil))
}

// DeleteTunnel drops the tunnel's connections first, since Cloudflare refuses to delete a tunnel that has any.
func (c *Client) DeleteTunnel(ctx context.Context, tunnelID string) error {
	if err := ignoreNotFound(c.call(ctx, http.MethodDelete, c.account("/cfd_tunnel/"+tunnelID+"/connections"), nil, nil)); err != nil {
		return err
	}
	return ignoreNotFound(c.call(ctx, http.MethodDelete, c.account("/cfd_tunnel/"+tunnelID), nil, nil))
}

func (c *Client) account(path string) string { return "/accounts/" + c.creds.AccountID + path }
func (c *Client) zone(path string) string    { return "/zones/" + c.creds.ZoneID + path }

type apiError struct {
	Status   int
	Messages []string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("Cloudflare API answered %d: %s", e.Status, strings.Join(e.Messages, "; "))
}

func withPermission(err error, action, permission string) error {
	var api *apiError
	if errors.As(err, &api) && (api.Status == http.StatusForbidden || api.Status == http.StatusUnauthorized) {
		return &PermissionError{Action: action, Permission: permission, Detail: strings.Join(api.Messages, "; ")}
	}
	return err
}

func ignoreNotFound(err error) error {
	var api *apiError
	if errors.As(err, &api) && api.Status == http.StatusNotFound {
		return nil
	}
	return err
}

func (c *Client) call(ctx context.Context, method, path string, body, result any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.creds.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var envelope struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("Cloudflare API answered %d with a body that is not JSON: %w", resp.StatusCode, err)
	}
	if resp.StatusCode >= 300 || !envelope.Success {
		apiErr := &apiError{Status: resp.StatusCode}
		for _, e := range envelope.Errors {
			apiErr.Messages = append(apiErr.Messages, fmt.Sprintf("%d %s", e.Code, e.Message))
		}
		return apiErr
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(envelope.Result, result)
}
