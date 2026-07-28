package provision

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type scriptedSSHCommander struct {
	responses map[string][]scriptedSSHResponse
	calls     []string
}

type scriptedSSHResponse struct {
	out string
	err error
}

func (c *scriptedSSHCommander) SSHCommand(command string) (string, error) {
	c.calls = append(c.calls, command)
	responses := c.responses[command]
	if len(responses) == 0 {
		return "", errors.New("unexpected SSH command: " + command)
	}
	c.responses[command] = responses[1:]
	return responses[0].out, responses[0].err
}

func newGoogleCOSProvisionerForTest(commander SSHCommander) *GoogleCOSProvisioner {
	p := &GoogleCOSProvisioner{}
	p.SSHCommander = commander
	return p
}

func TestGoogleCOSReadinessEnabled(t *testing.T) {
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		readinessMetadataCheck: {{out: "true\n"}},
	}}

	enabled, err := newGoogleCOSProvisionerForTest(commander).readinessEnabled()

	require.NoError(t, err)
	assert.True(t, enabled)
}

func TestGoogleCOSReadinessDisabled(t *testing.T) {
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		readinessMetadataCheck: {{out: ""}},
	}}

	enabled, err := newGoogleCOSProvisionerForTest(commander).readinessEnabled()

	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestGoogleCOSReadinessMetadataFailure(t *testing.T) {
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		readinessMetadataCheck: {{err: errors.New("metadata unavailable")}},
	}}

	enabled, err := newGoogleCOSProvisionerForTest(commander).readinessEnabled()

	require.Error(t, err)
	assert.False(t, enabled)
}

func TestDockerNetworkProbeShellSyntax(t *testing.T) {
	require.NoError(t, exec.Command("sh", "-n", "-c", dockerNetworkCheck).Run())
}

func TestReadinessMetadataCheckShellSyntax(t *testing.T) {
	require.NoError(t, exec.Command("sh", "-n", "-c", readinessMetadataCheck).Run())
}

func TestDockerNetworkDiagnosticsShellSyntax(t *testing.T) {
	require.NoError(t, exec.Command("sh", "-n", "-c", dockerNetworkDiagnosticsCmd).Run())
}

func TestGoogleCOSCloudInitFailure(t *testing.T) {
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		"sudo timeout 5m cloud-init status --wait --long": {{
			out: "status: error\ndetail: gpu-driver.service failed\n",
			err: errors.New("exit status 1"),
		}},
	}}

	err := newGoogleCOSProvisionerForTest(commander).waitForCloudInit()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "waiting for cloud-init readiness gate")
}

func TestVerifyDockerBridgeNetworkRequiresPreloadedImage(t *testing.T) {
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		dockerNetworkVerifierImageCheck: {{err: errors.New("No such image")}},
	}}

	err := newGoogleCOSProvisionerForTest(commander).verifyDockerBridgeNetworkWithInterval(0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "verifier image alpine:latest is not present")
	assert.Equal(t, 0, countCalls(commander.calls, "sudo systemctl -f restart docker"))
}

func TestDockerNetworkProbeCleansUpNamedContainer(t *testing.T) {
	assert.Contains(t, dockerNetworkCheck, "--name "+dockerNetworkProbeContainer)
	assert.Contains(t, dockerNetworkCheck, "docker rm -f "+dockerNetworkProbeContainer)
	assert.Contains(t, dockerNetworkCheck, "--pull=never")
}

func TestVerifyDockerBridgeNetworkHealthy(t *testing.T) {
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		dockerNetworkVerifierImageCheck: {{}},
		dockerNetworkCheck:              {{}},
	}}

	err := newGoogleCOSProvisionerForTest(commander).verifyDockerBridgeNetworkWithInterval(0)

	require.NoError(t, err)
	assert.Equal(t, []string{dockerNetworkVerifierImageCheck, dockerNetworkCheck}, commander.calls)
}

func TestVerifyDockerBridgeNetworkRepairsOnce(t *testing.T) {
	missing := make([]scriptedSSHResponse, 5)
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		dockerNetworkVerifierImageCheck:    {{}},
		dockerNetworkCheck:                 appendMissingThenSuccess(missing),
		dockerNetworkDiagnosticsCmd:        {{}},
		"sudo systemctl daemon-reload":     {{}},
		"sudo systemctl -f restart docker": {{}},
		"sudo docker version":              {{}},
	}}

	err := newGoogleCOSProvisionerForTest(commander).verifyDockerBridgeNetworkWithInterval(0)

	require.NoError(t, err)
	assert.Equal(t, 6, countCalls(commander.calls, dockerNetworkCheck))
	assert.Equal(t, 1, countCalls(commander.calls, "sudo systemctl -f restart docker"))
}

func TestVerifyDockerBridgeNetworkFailsClosed(t *testing.T) {
	missing := make([]scriptedSSHResponse, 10)
	for i := range missing {
		missing[i].err = errors.New("rules missing")
	}
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		dockerNetworkVerifierImageCheck:                    {{}},
		dockerNetworkCheck:                                 missing,
		dockerNetworkDiagnosticsCmd:                        {{}},
		"sudo systemctl daemon-reload":                     {{}},
		"sudo systemctl -f restart docker":                 {{}},
		"sudo docker version":                              {{}},
		"sudo systemctl stop docker.service docker.socket": {{}},
	}}

	err := newGoogleCOSProvisionerForTest(commander).verifyDockerBridgeNetworkWithInterval(0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "remained unavailable")
	assert.Equal(t, 1, countCalls(commander.calls, "sudo systemctl stop docker.service docker.socket"))
}

func appendMissingThenSuccess(responses []scriptedSSHResponse) []scriptedSSHResponse {
	for i := range responses {
		responses[i].err = errors.New("rules missing")
	}
	return append(responses, scriptedSSHResponse{})
}

func countCalls(calls []string, command string) int {
	count := 0
	for _, call := range calls {
		if call == command {
			count++
		}
	}
	return count
}
