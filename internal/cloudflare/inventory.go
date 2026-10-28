package cloudflare

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// NamedTunnel is a tunnel as the account lists it.
type NamedTunnel struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// DNSRecord is a record as the zone lists it.
type DNSRecord struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Name      string    `json:"name"`
	Content   string    `json:"content"`
	Comment   string    `json:"comment"`
	CreatedOn time.Time `json:"created_on"`
}

const pageSize = 1000

// ListTunnels returns every tunnel in the account that Cloudflare has not deleted.
func (c *Client) ListTunnels(ctx context.Context) ([]NamedTunnel, error) {
	return listAll[NamedTunnel](ctx, c, c.account("/cfd_tunnel"), url.Values{"is_deleted": {"false"}})
}

// ListCNAMERecords returns every CNAME record in the zone, the only type skybox creates.
func (c *Client) ListCNAMERecords(ctx context.Context) ([]DNSRecord, error) {
	return listAll[DNSRecord](ctx, c, c.zone("/dns_records"), url.Values{"type": {"CNAME"}})
}

// listAll reads one page after another until a page comes back short.
func listAll[T any](ctx context.Context, c *Client, path string, query url.Values) ([]T, error) {
	var all []T
	for page := 1; ; page++ {
		query.Set("per_page", strconv.Itoa(pageSize))
		query.Set("page", strconv.Itoa(page))
		var items []T
		if err := c.call(ctx, http.MethodGet, path+"?"+query.Encode(), nil, &items); err != nil {
			return nil, err
		}
		all = append(all, items...)
		if len(items) < pageSize {
			return all, nil
		}
	}
}

// Sandbox names the sandbox whose comment the record carries, which every record skybox creates does.
func (r DNSRecord) Sandbox() (string, bool) {
	return strings.CutPrefix(r.Comment, recordComment(""))
}
