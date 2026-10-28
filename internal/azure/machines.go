package azure

import "github.com/richardamare/skybox/internal/sandbox"

// Every profile is a v7 size, which boots only from an NVMe disk controller and needs accelerated networking.
type machine struct {
	vmSize string
	// family is the name Azure's usage API gives the quota this VM size draws from.
	family string
	vCPUs  int
}

var machines = map[sandbox.Size]machine{
	sandbox.Light:    {vmSize: "Standard_D2als_v7", family: "StandardDalsv7Family", vCPUs: 2},
	sandbox.Standard: {vmSize: "Standard_D4as_v7", family: "StandardDasv7Family", vCPUs: 4},
	sandbox.Large:    {vmSize: "Standard_D8as_v7", family: "StandardDasv7Family", vCPUs: 8},
}
