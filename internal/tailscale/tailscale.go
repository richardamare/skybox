// Package tailscale mints the auth key a sandbox joins the tailnet with, and deletes the sandbox's device again,
// through the Tailscale API. It signs in with the OAuth client from 1Password, whose secret never leaves this process:
// a sandbox only ever gets an auth key.
package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	// Tag is the tag every sandbox joins with. The tailnet policy lets the owner's devices reach it on 443, where
	// Tailscale Serve answers, and lets it reach nothing.
	Tag = "tag:skybox"
	// AuthKeyLifetime outlasts a sandbox's first boot, which spends the key. skybox up mints it before it creates the
	// VM, then waits up to 10 minutes for that boot.
	AuthKeyLifetime = 30 * time.Minute
)

// Credentials come from the Tailscale OAuth client item in the skybox 1Password vault. The client holds the scopes
// auth_keys and devices:core on Tag.
type Credentials struct {
	ClientID     string
	ClientSecret string
}

// Client calls the Tailscale API for the tailnet the OAuth client belongs to. It fetches one access token and reuses
// it, so it is not safe for concurrent use.
type Client struct {
	creds Credentials
	base  string
	http  *http.Client
	token string
}

func New(creds Credentials) *Client {
	return &Client{creds: creds, base: "https://api.tailscale.com/api/v2", http: http.DefaultClient}
}

// AuthKey is a key one sandbox joins the tailnet with. ID names it to the API, and Key is the secret itself.
type AuthKey struct {
	ID  string
	Key string
}

// Device is a machine on the tailnet.
type Device struct {
	// ID is the device's node ID.
	ID       string
	Hostname string
	// Name is the device's MagicDNS name, such as api-fix.tail1234.ts.net.
	Name string
	Tags []string
}

// ScopeError reports a call the OAuth client is not allowed to make, with the scope it lacks.
type ScopeError struct {
	Action string
	Scope  string
	Detail string
}

func (e *ScopeError) Error() string {
	return fmt.Sprintf("the Tailscale OAuth client cannot %s: give it the scope %s with the tag %s (%s)", e.Action, e.Scope, Tag, e.Detail)
}

// CredentialsError reports that Tailscale refused the OAuth client itself. It never carries the client secret.
type CredentialsError struct {
	Status int
	Detail string
}

func (e *CredentialsError) Error() string {
	return fmt.Sprintf("Tailscale refused the OAuth client (%d): %s", e.Status, e.Detail)
}

// MintAuthKey creates the key one sandbox joins with: tagged, pre-approved, single use and short-lived. It is not
// ephemeral, since Tailscale removes an ephemeral device once it goes offline, and a sleeping sandbox is offline.
func (c *Client) MintAuthKey(ctx context.Context, sandbox string) (AuthKey, error) {
	body := map[string]any{
		"capabilities": map[string]any{"devices": map[string]any{"create": map[string]any{
			"reusable":      false,
			"ephemeral":     false,
			"preauthorized": true,
			"tags":          []string{Tag},
		}}},
		"expirySeconds": int(AuthKeyLifetime.Seconds()),
		"description":   "skybox " + sandbox,
	}
	var key struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := c.call(ctx, http.MethodPost, "/tailnet/-/keys", body, &key); err != nil {
		return AuthKey{}, withScope(err, "create an auth key", "auth_keys")
	}
	return AuthKey{ID: key.ID, Key: key.Key}, nil
}

// RevokeAuthKey deletes a key that no sandbox will spend. A key already gone is not an error.
func (c *Client) RevokeAuthKey(ctx context.Context, id string) error {
	err := ignoreNotFound(c.call(ctx, http.MethodDelete, "/tailnet/-/keys/"+url.PathEscape(id), nil, nil))
	return withScope(err, "revoke an auth key", "auth_keys")
}

// Devices returns the sandbox's devices: those tagged with Tag whose hostname or MagicDNS name is the sandbox's name.
// There is more than one only when an earlier sandbox of that name left its device behind.
func (c *Client) Devices(ctx context.Context, sandbox string) ([]Device, error) {
	var list struct {
		Devices []struct {
			NodeID   string   `json:"nodeId"`
			Hostname string   `json:"hostname"`
			Name     string   `json:"name"`
			Tags     []string `json:"tags"`
		} `json:"devices"`
	}
	if err := c.call(ctx, http.MethodGet, "/tailnet/-/devices", nil, &list); err != nil {
		return nil, withScope(err, "list devices", "devices:core")
	}
	var devices []Device
	for _, d := range list.Devices {
		name := strings.TrimSuffix(d.Name, ".")
		label, _, _ := strings.Cut(name, ".")
		if (d.Hostname == sandbox || label == sandbox) && slices.Contains(d.Tags, Tag) {
			devices = append(devices, Device{ID: d.NodeID, Hostname: d.Hostname, Name: name, Tags: d.Tags})
		}
	}
	return devices, nil
}

// DeleteDevices removes every device of the sandbox from the tailnet.
func (c *Client) DeleteDevices(ctx context.Context, sandbox string) error {
	devices, err := c.Devices(ctx, sandbox)
	if err != nil {
		return err
	}
	for _, d := range devices {
		err := ignoreNotFound(c.call(ctx, http.MethodDelete, "/device/"+url.PathEscape(d.ID), nil, nil))
		if err != nil {
			return withScope(err, "delete a device", "devices:core")
		}
	}
	return nil
}

type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("Tailscale API answered %d: %s", e.Status, e.Message)
}

func withScope(err error, action, scope string) error {
	var api *apiError
	if errors.As(err, &api) && api.Status == http.StatusForbidden {
		return &ScopeError{Action: action, Scope: scope, Detail: api.Message}
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

// accessToken trades the OAuth client's ID and secret for an access token. The secret goes only into this request's
// form body, which no error repeats.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	if c.token != "" {
		return c.token, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.creds.ClientID},
		"client_secret": {c.creds.ClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", &CredentialsError{Status: resp.StatusCode, Detail: message(resp.Body)}
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil || token.AccessToken == "" {
		return "", &CredentialsError{Status: resp.StatusCode, Detail: "the answer holds no access token"}
	}
	c.token = token.AccessToken
	return c.token, nil
}

func (c *Client) call(ctx context.Context, method, path string, body, result any) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
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
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return &apiError{Status: resp.StatusCode, Message: message(resp.Body)}
	}
	if result == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("Tailscale API answered %d with a body that is not the expected JSON: %w", resp.StatusCode, err)
	}
	return nil
}

// message reads the reason from an error answer. It takes only the fields Tailscale fills with its own words, so it
// never repeats what the request sent.
func message(body io.Reader) string {
	var answer struct {
		Message          string `json:"message"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.NewDecoder(body).Decode(&answer)
	var parts []string
	for _, part := range []string{answer.Message, answer.Error, answer.ErrorDescription} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return "no reason given"
	}
	return strings.Join(parts, ": ")
}
