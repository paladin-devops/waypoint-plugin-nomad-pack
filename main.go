package main

import (
	sdk "github.com/hashicorp/waypoint-plugin-sdk"
	"github.com/paladin-devops/waypoint-plugin-nomad-pack/platform"
)

func main() {
	sdk.Main(sdk.WithComponents(
		&platform.Platform{},
	))
}
