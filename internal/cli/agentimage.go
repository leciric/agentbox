package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"agentbox/internal/agentimage"
	"agentbox/internal/api"
)

// addAgentImageCmd puts the container image agents run as under the image
// command, where it belongs: the image command makes the Incus base an agent's
// machine is copied from, and this one makes the image a pod runs.
func addAgentImageCmd(root *cobra.Command, a *app) {
	for _, cmd := range root.Commands() {
		if cmd.Name() == "image" {
			cmd.AddCommand(newAgentImageCmd(a))
			return
		}
	}
}

// newAgentImageCmd is "agentbox image agent": the container image an agent
// runs as when its machine is a pod in a Kubernetes cluster.
func newAgentImageCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Build the container image agents run as in a Kubernetes cluster",
		Long: `The image a pod runs, as opposed to the Incus base machine an agent's machine
is copied from on this host.

It is built on the machine the source is on, with Docker, or with Podman when
Docker is not there, not through the daemon: the daemon is asked only for a
registry's address. A push signs in with the login the runtime already has
("docker login"), and this command never takes a credential, as a flag or
anywhere else.

The image is linux/amd64, like the binary scripts/build-agent-image.sh builds.
It carries the agent binary, the desktop an agent's browser and media work runs
on, and the AI tools and ACP adapters at the pins internal/image/tools.txt
holds, OpenCode 2 among them at image.OpenCode2Version. It is for your own
cluster and your own registry, and the publishable variant, which could go to a
public registry, is not made here yet: --push refuses a registry added with
--public.`,
	}
	cmd.AddCommand(newAgentImageBuildCmd(a))
	return cmd
}

func newAgentImageBuildCmd(a *app) *cobra.Command {
	var (
		tag          string
		stage        string
		push         bool
		registryName string
	)
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build the image, and push it to a registry with --push",
		Long: `Builds internal/agentimage/Dockerfile from the repository root, copying the
agentbox binary into bin/agentbox first, and tags it agentbox-agent:<tag> or
<registry url>/agentbox-agent:<tag>.

  agentbox image agent build --tag dev
  agentbox image agent build --stage dev
  agentbox image agent build --tag 0.3.0 --registry forgejo --push`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := imageStage(stage)
			if err != nil {
				return err
			}
			if push && registryName == "" {
				return errors.New("--push needs --registry: it names the registry the image goes to")
			}
			ref := "agentbox-agent:" + tag
			if registryName != "" {
				c, err := a.client(cmd)
				if err != nil {
					return err
				}
				registry, err := c.Registry(cmd.Context(), registryName)
				if err != nil {
					return err
				}
				if registry.URL == "" {
					return fmt.Errorf("registry %s has no address: add it again with agentbox registry add", registryName)
				}
				if push {
					if err := checkPushTo(registry, stage); err != nil {
						return err
					}
				}
				ref = strings.TrimSuffix(registry.URL, "/") + "/" + ref
			}
			root, err := repositoryRoot()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Building %s from %s\n", ref, root)
			opts := agentimage.Options{Context: root, Image: ref, Stage: target}
			if err := agentimage.Build(cmd.Context(), opts); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "Built %s\n", ref)
			if !push {
				_, _ = fmt.Fprintf(out, "\nRun it with: docker run --rm %s agentbox version\n", ref)
				return nil
			}
			digest, err := agentimage.Push(cmd.Context(), ref)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "Pushed %s\nDigest %s\n", ref, strings.TrimPrefix(digest, "@"))
			return nil
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "dev", "the image's tag, after agentbox-agent")
	cmd.Flags().StringVar(&stage, "stage", "agent", "the stage to build: agent (the image a pod runs) or dev (the system and the binary, no user)")
	cmd.Flags().BoolVar(&push, "push", false, "push the image to the registry --registry names")
	cmd.Flags().StringVar(&registryName, "registry", "", "the registry to name the image under (agentbox registry list), and to push it to with --push")
	return cmd
}

// publishableStages are the Dockerfile stages that may go to a registry anyone
// can pull from. None yet: every stage is built on base, which installs Claude
// Code and the ACP adapters, and those may not be redistributed. A stage
// without them is added here once the Dockerfile has one.
var publishableStages = map[string]bool{}

// checkPushTo refuses to push stage to a public registry unless the stage is
// one that may be published. A private registry takes any stage: it is the
// default, and what the image is for.
func checkPushTo(registry api.Registry, stage string) error {
	if !registry.Public || publishableStages[stage] {
		return nil
	}
	return fmt.Errorf("registry %s is public, and the %s image carries Claude Code and the ACP adapters, which may not be redistributed: "+
		"push it to a private registry (agentbox registry add <name> --url <url>, without --public)", registry.Name, stageName(stage))
}

// stageName is the name --stage gives a stage, agent for the whole file.
func stageName(stage string) string {
	if stage == "" {
		return "agent"
	}
	return stage
}

// imageStage checks --stage and returns the Dockerfile stage to stop at, empty
// for the whole file.
func imageStage(stage string) (string, error) {
	switch stage {
	case "", "dev", "agent":
		return stage, nil
	}
	return "", fmt.Errorf("--stage is agent or dev, not %q", stage)
}

// repositoryRoot is the build context: the repository the Dockerfile is in,
// found from the working directory so the command works from a subdirectory
// too.
func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, agentimage.Dockerfile)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("this isn't the agentbox repository: internal/agentimage/Dockerfile is nowhere above the working directory")
		}
		dir = parent
	}
}
