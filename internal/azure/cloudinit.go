package azure

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"text/template"
)

// t3Version is the T3 Code server every new sandbox installs. Raise it to the desktop's version when a release needs it.
const t3Version = "0.1.4-nightly.20261028.3814"

//go:embed cloud-init.yaml.tmpl
var cloudInitTemplate string

var cloudInit = template.Must(template.New("cloud-init").Funcs(template.FuncMap{
	"json": func(s string) (string, error) {
		b, err := json.Marshal(s)
		return string(b), err
	},
}).Parse(cloudInitTemplate))

type cloudInitData struct {
	// Name is the sandbox's name, which it joins the tailnet under.
	Name             string
	SSHKeys          []string
	TunnelToken      string
	TailnetAuthKey   string
	KeyVaultName     string
	IdentityClientID string
	T3Version        string
}

func renderCloudInit(data cloudInitData) ([]byte, error) {
	var out bytes.Buffer
	if err := cloudInit.Execute(&out, data); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
