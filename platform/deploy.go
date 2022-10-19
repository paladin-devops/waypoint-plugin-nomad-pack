package platform

import (
	"context"
	"encoding/json"
	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/waypoint-plugin-sdk/component"
	"github.com/hashicorp/waypoint-plugin-sdk/framework/resource"
	sdk "github.com/hashicorp/waypoint-plugin-sdk/proto/gen"
	"github.com/hashicorp/waypoint-plugin-sdk/terminal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"os/exec"
	"strings"
)

type DeployConfig struct {
	DeploymentName string `hcl:"deployment_name,attr"` // name to give the deployed instance of the Nomad Pack

	RegistryName   string `hcl:"registry_name,attr"`       // registry name
	RegistrySource string `hcl:"registry_source,attr"`     // registry url
	RegistryRef    string `hcl:"registry_ref,optional"`    // specific git ref of a registry
	RegistryTarget string `hcl:"registry_target,optional"` // specific pack within a registry

	Variables     map[string]string `hcl:"variables,optional"`      // variables to supply to the pack
	VariableFiles []string          `hcl:"variable_files,optional"` // variable files

	Pack string `hcl:"pack,attr"` // name of the Nomad Pack to run
}

type NomadPack struct {
	PackName       string
	RegistryName   string
	DeploymentName string
	JobName        string
	Status         string
}

type Platform struct {
	config DeployConfig
}

func (p *Platform) Config() (interface{}, error) {
	return &p.config, nil
}

func (p *Platform) ConfigSet(config interface{}) error {
	return nil
}

// Resource manager will tell the Waypoint Plugin SDK how to create and destroy
// Nomad Packs for your deployments.
func (p *Platform) resourceManager(log hclog.Logger, dcr *component.DeclaredResourcesResp, dtr *component.DestroyedResourcesResp) *resource.Manager {
	return resource.NewManager(
		resource.WithLogger(log.Named("resource_manager")),
		resource.WithDeclaredResourcesResp(dcr),
		resource.WithDestroyedResourcesResp(dtr),
		resource.WithResource(resource.NewResource(
			resource.WithName("nomad_pack"),
			resource.WithState(&Resource_Pack{}),
			resource.WithCreate(p.deployPack),
			resource.WithDestroy(p.destroyPack),
			resource.WithStatus(p.packStatus),
			resource.WithPlatform("nomad"),
			resource.WithCategoryDisplayHint(sdk.ResourceCategoryDisplayHint_INSTANCE_MANAGER),
		)),
	)
}

// DeployFunc implements component.Platform
func (p *Platform) DeployFunc() interface{} {
	return p.deploy
}

// GenerationFunc implements component.Generation
func (p *Platform) GenerationFunc() interface{} {
	return p.generation
}

// StatusFunc implements component.Status
func (p *Platform) StatusFunc() interface{} {
	return p.status
}

func (p *Platform) deploy(
	ctx context.Context,
	ui terminal.UI,
	log hclog.Logger,
	dcr *component.DeclaredResourcesResp,
) (*Deployment, error) {
	u := ui.Status()
	defer u.Close()
	u.Update("Deploy application")

	var result Deployment

	// Create our resource manager and create deployment resources
	rm := p.resourceManager(log, dcr, nil)

	if err := rm.CreateAll(
		ctx, log, u, ui, &result,
	); err != nil {
		return nil, err
	}

	result.ResourceState = rm.State()

	// Store our resource state
	packState := rm.Resource("nomad_pack").State().(*Resource_Pack)
	if packState == nil {
		return nil, status.Errorf(codes.Internal, "pack state is nil, this shouldn't happen")
	}

	u.Update("Application deployed")

	return &result, nil
}

func (p *Platform) generation(
	ctx context.Context,
	log hclog.Logger,
	ui terminal.UI,
) ([]byte, error) {
	refArg, err := p.addRegistry(ui)
	if err != nil {
		return nil, err
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
		return nil, err
	}
	log.Info(string(output[:]))
	nomadPackStatusOutput := string(output[:])
	statusLines := strings.Split(nomadPackStatusOutput, "\n") // we only want the 3rd line
	statusFields := strings.Split(statusLines[2], "|")        // the 3rd line of output contains the pack info

	// If there are > 3 fields here, that means a Pack deployment with the specified
	// registry, pack name, and deployment name was returned.
	if len(statusFields) > 3 {
		return []byte(statusFields[3]), nil
	}
	return nil, nil
}

// status creates the status report for the deployment and associated resources
func (p *Platform) status(
	ctx context.Context,
	ji *component.JobInfo,
	ui terminal.UI,
	log hclog.Logger,
	deployment *Deployment,
) (*sdk.StatusReport, error) {
	sg := ui.StepGroup()
	s := sg.Add("Checking the status of the deployment...")

	rm := p.resourceManager(log, nil, nil)

	// If we don't have resource state, this state is from an older version
	// and we need to manually recreate it.
	if deployment.ResourceState == nil {
		err := rm.Resource("nomad_pack").SetState(&Resource_Pack{
			Name: deployment.Id,
		})
		if err != nil {
			log.Error("error setting resource state")
			return nil, err
		}
	} else {
		// Load our set state
		if err := rm.LoadState(deployment.ResourceState); err != nil {
			return nil, err
		}
	}

	// This will call the StatusReport func on every defined resource in ResourceManager
	report, err := rm.StatusReport(ctx, log, sg, ui)
	if err != nil {
		log.Error("error generating status report: " + err.Error())
		return nil, status.Errorf(codes.Internal, "resource manager failed to generate resource statuses: %s", err)
	}

	report.Health = sdk.StatusReport_READY
	s.Done()

	return report, nil
}

func (p *Platform) deployPack(
	ctx context.Context,
	log hclog.Logger,
	st terminal.Status,
	ui terminal.UI,
	result *Deployment,
	state *Resource_Pack,
) error {
	refArg, err := p.addRegistry(ui)
	if err != nil {
		return err
	}

	runArgs := []string{
		"run",
		p.config.Pack,
		"--name=" + p.config.DeploymentName,
		"--registry=" + p.config.RegistryName,
	}

	if refArg != "" {
		runArgs = append(runArgs, refArg)
	}

	runArgs = p.setVarArgs(runArgs)

	runCmd := exec.Command("nomad-pack", runArgs...)
	output, err := runCmd.Output()
	if err != nil {
		ui.Output(string(output[:]), terminal.WithErrorStyle())
		ui.Output("Error running pack: %s", err, terminal.WithErrorStyle())
		return err
	}
	ui.Output(string(output[:]), terminal.WithInfoStyle())
	state.Name = p.config.Pack

	return nil
}

func (p *Platform) packStatus(
	ctx context.Context,
	ui terminal.UI,
	pack *Resource_Pack,
	sr *resource.StatusResponse,
) error {
	packResource := &sdk.StatusReport_Resource{
		Id:                  pack.Name,
		Name:                pack.Name,
		CreatedTime:         timestamppb.Now(),
		CategoryDisplayHint: sdk.ResourceCategoryDisplayHint_INSTANCE,
	}
	refArg, err := p.addRegistry(ui)
	if err != nil {
		return err
	}
	// Determine health status of "this" resource.
	statusArgs := []string{
		"status",
		pack.Name,
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

	// Print the raw output of the status check to the user
	ui.Output(string(output[:]), terminal.WithInfoStyle())
	nomadPackStatusOutput := string(output[:])

	statusLines := strings.Split(nomadPackStatusOutput, "\n") // we only want the 3rd line
	statusFields := strings.Split(statusLines[2], "|")        // the 3rd line of output contains the pack info
	packStatus := NomadPack{
		PackName:       statusFields[0],
		RegistryName:   statusFields[1],
		DeploymentName: statusFields[2],
		JobName:        statusFields[3],
		Status:         statusFields[4],
	}

	if strings.Contains(packStatus.Status, "running") {
		packResource.Health = sdk.StatusReport_READY
		packResource.HealthMessage = "pack is running"
	} else if strings.Contains(packStatus.Status, "pending") {
		packResource.Health = sdk.StatusReport_DOWN
		packResource.HealthMessage = "pack is pending"
	} else {
		packResource.Health = sdk.StatusReport_UNKNOWN
		packResource.HealthMessage = "unknown pack status"
	}

	jsonStatus, err := json.Marshal(packStatus)
	if err != nil {
		return err
	}
	packResource.StateJson = string(jsonStatus)

	sr.Resources = append(sr.Resources, packResource)
	return nil
}

// addRegistry adds the Nomad Pack registry to the runner. Multiple functions
// use this to ensure that the given registry is added prior to performing any
// operations.
func (p *Platform) addRegistry(ui terminal.UI) (string, error) {
	args := []string{
		"registry",
		"add",
		p.config.RegistryName,
		p.config.RegistrySource,
	}
	if p.config.RegistryTarget != "" {
		args = append(args, "--target="+p.config.RegistryTarget)
	}
	var registryRef string
	if p.config.RegistryRef != "" {
		registryRef = "--ref=" + p.config.RegistryRef
		args = append(args, registryRef)
	}
	registryCmd := exec.Command("nomad-pack", args...)
	output, err := registryCmd.Output()
	if err != nil {
		ui.Output(string(output[:]), terminal.WithErrorStyle())
		ui.Output("Error adding pack registry: %s", err, terminal.WithErrorStyle())
		return "", err
	}
	return registryRef, nil
}

// setVarArgs sets key value pairs of variable overrides and variable file overrides
// to be used by Nomad Pack commands
func (p *Platform) setVarArgs(args []string) []string {
	// Set our variable overrides
	for varName, varVal := range p.config.Variables {
		args = append(args, "--var="+varName+"="+varVal)
	}

	// Set our variable file overrides
	for _, varFile := range p.config.VariableFiles {
		args = append(args, "--var-file="+varFile)
	}
	return args
}
