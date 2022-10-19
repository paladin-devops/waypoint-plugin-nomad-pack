# Nomad Pack Waypoint Plugin

Plugin for Hashicorp Waypoint to deploy a Nomad Pack to a Nomad cluster.

## Description

This plugin requires that the [Nomad Pack](https://github.com/hashicorp/nomad-pack) 
binary be installed on the runner's PATH. The plugin itself executes `nomad-pack` 
commands, including `registry add`, `run`, `status`, and `destroy`. Public or private
Nomad Pack registries may be configured for use, and the plugin will deploy the 
configured pack from the registry to your Nomad cluster. Presently, the Nomad 
cluster targeted for deployment relies on environment variables that Nomad Pack
uses, including `NOMAD_ADDR`, `NOMAD_TOKEN`, etc.

If you're using Waypoint with a local runner (either with the `-local` flag, or you
haven't configured remote operations for your project), then these environment variables
may be set in the same terminal where Waypoint is running. If you're using remote 
operations, they may be set via `waypoint config set -runner`, as demonstrated below:

`waypoint config set -runner -scope=global NOMAD_ADDR=http://127.0.0.1:4646 NOMAD_TOKEN=my-cool-nomad-token`

## Usage

### Inputs

- `deployment_name` = name to give the deployed instance of the Nomad Pack, required.
- `pack` = the name of the Nomad Pack to deploy from the specified registry, required.
- `registry_name` = the desired name of the Nomad Pack registry, required.
- `registry_ref` = the Git ref of the Nomad Pack registry, optional.
- `registry_source` = the URL of the Nomad Pack registry, required.
- `registry_target` = a specific Nomad Pack within a registry to add, optional.
- `variables` = Nomad Pack variable overrides, optional.
- `variable_files` = path to a Nomad Pack variable override file, optional.

### Example

#### waypoint.hcl File
```hcl
project = "hello-world"

app "hello-world-pack" {
   build {
      use "docker" {}
   }

   deploy {
      use "nomad-pack" {
         deployment_name = "hello_paladin_devops"
         pack            = "hello_world"
         registry_name   = "default"
         registry_ref    = "main"
         registry_source = "github.com/hashicorp/nomad-pack-community-registry"
         registry_target = "hello_world"
         variables = {
           "job_name": "paladin_devops"
         }
         variable_files = [
           "vars.hcl"
         ]
      }
   }
}