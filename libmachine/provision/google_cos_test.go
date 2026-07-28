package provision

import (
	"errors"
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

func TestVerifyDockerBridgeNetworkHealthy(t *testing.T) {
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		dockerNetworkRulesCheck: {{}},
	}}

	err := newGoogleCOSProvisionerForTest(commander).verifyDockerBridgeNetworkWithInterval(0)

	require.NoError(t, err)
	assert.Equal(t, []string{dockerNetworkRulesCheck}, commander.calls)
}

func TestVerifyDockerBridgeNetworkRepairsOnce(t *testing.T) {
	missing := make([]scriptedSSHResponse, 5)
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		dockerNetworkRulesCheck: appendMissingThenSuccess(missing),
		`sudo sh -c 'echo "docker-network-readiness: Docker bridge rules missing"; systemctl show docker.service iptables-restore.service gpu-driver.service -p Id -p ActiveEnterTimestamp -p ExecMainStartTimestamp; iptables -t nat -S POSTROUTING; iptables -S FORWARD'`: {{}},
		"sudo systemctl daemon-reload":     {{}},
		"sudo systemctl -f restart docker": {{}},
		"sudo docker version":              {{}},
	}}

	err := newGoogleCOSProvisionerForTest(commander).verifyDockerBridgeNetworkWithInterval(0)

	require.NoError(t, err)
	assert.Equal(t, 6, countCalls(commander.calls, dockerNetworkRulesCheck))
	assert.Equal(t, 1, countCalls(commander.calls, "sudo systemctl -f restart docker"))
}

func TestVerifyDockerBridgeNetworkFailsClosed(t *testing.T) {
	missing := make([]scriptedSSHResponse, 10)
	for i := range missing {
		missing[i].err = errors.New("rules missing")
	}
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		dockerNetworkRulesCheck: missing,
		`sudo sh -c 'echo "docker-network-readiness: Docker bridge rules missing"; systemctl show docker.service iptables-restore.service gpu-driver.service -p Id -p ActiveEnterTimestamp -p ExecMainStartTimestamp; iptables -t nat -S POSTROUTING; iptables -S FORWARD'`: {{}},
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
