package platform

import (
	"context"
	"os/exec"
	"strings"

	hclog "github.com/hashicorp/go-hclog"

	"github.com/hashicorp/waypoint-plugin-sdk/component"
	"github.com/hashicorp/waypoint-plugin-sdk/terminal"
)

// DestroyFunc implements the Destroyer interface
func (p *Platform) DestroyFunc() interface{} {
	return p.destroy
}

func (p *Platform) destroy(
	ctx context.Context,
	ui terminal.UI,
	log hclog.Logger,
	deployment *Deployment,
	dcr *component.DeclaredResourcesResp,
	dtr *component.DestroyedResourcesResp,
) error {
	sg := ui.StepGroup()
	defer sg.Wait()

	rm := p.resourceManager(log, dcr, dtr)

	// If we don't have resource state, this state is from an older version
	// and we need to manually recreate it.
	if deployment.ResourceState == nil {
		err := rm.Resource("nomad_pack").SetState(&Resource_Pack{
			Name: deployment.Name,
		})
		if err != nil {
			return err
		}
	} else {
		// Load our set state
		if err := rm.LoadState(deployment.ResourceState); err != nil {
			return err
		}
	}

	// Destroy
	return rm.DestroyAll(ctx, log, sg, ui)
}

func (p *Platform) destroyPack(
	ctx context.Context,
	log hclog.Logger,
	ui terminal.UI,
	state *Resource_Pack,
) error {
	refArg, err := p.addRegistry(ui)
	if err != nil {
		return err
	}

	statusArgs := []string{
		"status",
		p.config.Pack,
		"--registry=" + p.config.RegistryName,
		"--name=" + p.config.DeploymentName,
	}
	if refArg != "" {
		statusArgs = append(statusArgs, refArg)
	}

	statusCmd := exec.Command("nomad-pack", statusArgs...)
	output, err := statusCmd.Output()
	if err != nil {
		ui.Output("Error getting pack status: %s", err, terminal.WithErrorStyle())
		return err
	}
	log.Info(string(output[:]))
	nomadPackStatusOutput := string(output[:])
	statusLines := strings.Split(nomadPackStatusOutput, "\n") // we only want the 3rd line
	statusFields := strings.Split(statusLines[2], "|")        // the 3rd line of output contains the pack info

	// If there are > 3 fields here, that means a Pack deployment with the specified
	// registry, pack name, and deployment name was returned.
	if len(statusFields) <= 3 {
		log.Info("no pack to destroy, skipping redundant destroy operation")
		ui.Output("No pack to destroy, skipping redundant destroy operation", terminal.WithInfoStyle())
		return nil
	}

	destroyArgs := []string{
		"destroy",
		p.config.Pack,
		"--name=" + p.config.DeploymentName,
		"--registry=" + p.config.RegistryName,
	}

	if refArg != "" {
		destroyArgs = append(destroyArgs, refArg)
	}

	destroyArgs = p.setVarArgs(destroyArgs)

	destroyCmd := exec.Command("nomad-pack", destroyArgs...)
	output, err = destroyCmd.Output()
	if err != nil {
		ui.Output(string(output[:]), terminal.WithErrorStyle())
		ui.Output("Error destroying pack: %s", err, terminal.WithErrorStyle())
		return err
	}
	ui.Output(string(output[:]), terminal.WithInfoStyle())
	return nil
}
