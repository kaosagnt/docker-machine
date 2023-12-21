package mcndockerclient

import (
	"context"
	"fmt"
	"net/http"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/machine/libmachine/cert"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// DockerClient creates a docker client for a given host.
func DockerClient(dockerHost DockerHost) (*client.Client, error) {
	url, err := dockerHost.URL()
	if err != nil {
		return nil, err
	}

	tlsConfig, err := cert.ReadTLSConfig(url, dockerHost.AuthOptions())
	if err != nil {
		return nil, fmt.Errorf("Unable to read TLS config: %s", err)
	}

	httpClient := &http.Client{
		Transport:     &http.Transport{TLSClientConfig: tlsConfig},
		CheckRedirect: client.CheckRedirect,
	}

	return client.NewClientWithOpts(
		client.WithHTTPClient(httpClient),
		client.WithHost(url),
		client.WithAPIVersionNegotiation(),
	)
}

// CreateContainer creates a docker container.
func CreateContainer(dockerHost DockerHost, config *container.Config, hostConfig *container.HostConfig, name string) error {
	docker, err := DockerClient(dockerHost)
	if err != nil {
		return err
	}

	if _, err = docker.ImagePull(context.Background(), config.Image, types.ImagePullOptions{}); err != nil {
		return fmt.Errorf("Unable to pull image: %s", err)
	}

	containerCreateResp, err := docker.ContainerCreate(context.Background(), config, hostConfig, &network.NetworkingConfig{}, &v1.Platform{}, name)
	if err != nil {
		return fmt.Errorf("Error while creating container: %s", err)
	}

	if err = docker.ContainerStart(context.Background(), containerCreateResp.ID, types.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("Error while starting container: %s", err)
	}

	return nil
}
