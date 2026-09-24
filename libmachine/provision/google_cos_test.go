package provision

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testReadinessURL = "https://gitlab.com/readiness"

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
		metadataAttributeCheck(readinessGateMetadataKey): {{out: "true\n"}},
	}}

	enabled, err := newGoogleCOSProvisionerForTest(commander).readinessEnabled()

	require.NoError(t, err)
	assert.True(t, enabled)
}

func TestGoogleCOSReadinessDisabled(t *testing.T) {
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		metadataAttributeCheck(readinessGateMetadataKey): {{out: ""}},
	}}

	enabled, err := newGoogleCOSProvisionerForTest(commander).readinessEnabled()

	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestGoogleCOSReadinessMetadataFailure(t *testing.T) {
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		metadataAttributeCheck(readinessGateMetadataKey): {{err: errors.New("metadata unavailable")}},
	}}

	enabled, err := newGoogleCOSProvisionerForTest(commander).readinessEnabled()

	require.Error(t, err)
	assert.False(t, enabled)
}

func TestGoogleCOSReadinessURLSet(t *testing.T) {
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		metadataAttributeCheck(readinessURLMetadataKey): {{out: "https://gitlab.com/readiness\n"}},
	}}

	url, err := newGoogleCOSProvisionerForTest(commander).readinessURL()

	require.NoError(t, err)
	assert.Equal(t, "https://gitlab.com/readiness", url)
}

func TestGoogleCOSReadinessURLUnset(t *testing.T) {
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		metadataAttributeCheck(readinessURLMetadataKey): {{out: ""}},
	}}

	url, err := newGoogleCOSProvisionerForTest(commander).readinessURL()

	require.NoError(t, err)
	assert.Empty(t, url)
}

func TestDockerNetworkProbeShellSyntax(t *testing.T) {
	require.NoError(t, exec.Command("sh", "-n", "-c", dockerNetworkCheck("https://gitlab.com/readiness")).Run())
}

func TestShellQuoteSurvivesTwoShellLayers(t *testing.T) {
	dir := t.TempDir()
	marker := dir + "/pwned"
	inputs := []string{
		"https://gitlab.com/readiness",
		"https://gitlab.com/health?check=1&token=abc",
		"https://gitlab.com/a'b",
		"https://gitlab.com/';touch " + marker + ";'",
		"https://gitlab.com/$(touch " + marker + ")",
		"https://gitlab.com/`touch " + marker + "`",
		`https://gitlab.com/x\'y`,
	}

	for _, in := range inputs {
		out := dir + "/out"
		inner := "sh -c 'printf %s \"$1\" >" + out + "' probe " + shellQuote(in)
		require.NoError(t, exec.Command("sh", "-c", inner).Run(), in)

		got, err := os.ReadFile(out)
		require.NoError(t, err)
		assert.Equal(t, in, string(got), in)
		assert.NoFileExists(t, marker, in)
	}
}

func TestReadinessMetadataCheckShellSyntax(t *testing.T) {
	require.NoError(t, exec.Command("sh", "-n", "-c", metadataAttributeCheck(readinessGateMetadataKey)).Run())
}

func TestDockerNetworkDiagnosticsShellSyntax(t *testing.T) {
	require.NoError(t, exec.Command("sh", "-n", "-c", dockerNetworkDiagnosticsCmd).Run())
}

func TestGoogleCOSCloudInitFailure(t *testing.T) {
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		cloudInitWaitCmd(cloudInitResultFile, cloudInitWaitTimeout): {{
			out: "status: error\ndetail: gpu-driver.service failed\n",
			err: errors.New("exit status 1"),
		}},
	}}

	err := newGoogleCOSProvisionerForTest(commander).waitForCloudInit()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "waiting for cloud-init readiness gate")
}

func TestGoogleCOSCloudInitDegradedDone(t *testing.T) {
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		cloudInitWaitCmd(cloudInitResultFile, cloudInitWaitTimeout): {{
			out: "status: done\nextended_status: degraded done\nrecoverable_errors:\nWARNING:\n\t- Getting data from DataSourceGCELocal failed\n",
		}},
	}}

	require.NoError(t, newGoogleCOSProvisionerForTest(commander).waitForCloudInit())
}

func TestGoogleCOSCloudInitWaitShellSyntax(t *testing.T) {
	require.NoError(t, exec.Command("sh", "-n", "-c", cloudInitWaitCmd(cloudInitResultFile, cloudInitWaitTimeout)).Run())
}

// Runs the remote shell snippet with fake sudo and cloud-init: once result.json
// exists, cloud-init's exit 0 and 2 (degraded done) pass and 1 is returned as is;
// without the file the snippet times out with 124 and never consults the exit code.
func TestGoogleCOSCloudInitWaitExitCodes(t *testing.T) {
	for _, tt := range []struct {
		name          string
		resultFile    bool
		cloudInitExit int
		wantExit      int
	}{
		{"done", true, 0, 0},
		{"degraded done", true, 2, 0},
		{"error", true, 1, 1},
		{"timeout", false, 0, 124},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFakeCommand(t, dir, "sudo", "#!/bin/sh\nexec \"$@\"\n")
			writeFakeCommand(t, dir, "cloud-init", fmt.Sprintf("#!/bin/sh\nexit %d\n", tt.cloudInitExit))
			resultFile := filepath.Join(dir, "result.json")
			if tt.resultFile {
				require.NoError(t, os.WriteFile(resultFile, []byte("{}"), 0o644))
			}

			cmd := exec.Command("sh", "-c", cloudInitWaitCmd(resultFile, 3*time.Second))
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
			err := cmd.Run()

			if tt.wantExit == 0 {
				assert.NoError(t, err)
				return
			}
			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, tt.wantExit, exitErr.ExitCode())
		})
	}
}

func writeFakeCommand(t *testing.T, dir, name, script string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755))
}

func TestVerifyDockerBridgeNetworkRequiresPreloadedImage(t *testing.T) {
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		dockerNetworkVerifierImageCheck: {{err: errors.New("No such image")}},
	}}

	err := newGoogleCOSProvisionerForTest(commander).verifyDockerBridgeNetworkWithInterval(testReadinessURL, 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "verifier image alpine:latest is not present")
	assert.Equal(t, []string{dockerNetworkVerifierImageCheck}, commander.calls)
}

func TestDockerNetworkProbeCleansUpNamedContainer(t *testing.T) {
	check := dockerNetworkCheck(testReadinessURL)
	assert.Contains(t, check, "--name "+dockerNetworkProbeContainer)
	assert.Contains(t, check, "docker rm -f "+dockerNetworkProbeContainer)
	assert.Contains(t, check, "--pull=never")
	assert.Contains(t, check, testReadinessURL)
}

func TestVerifyDockerBridgeNetworkHealthy(t *testing.T) {
	check := dockerNetworkCheck(testReadinessURL)
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		dockerNetworkVerifierImageCheck: {{}},
		check:                           {{}},
	}}

	err := newGoogleCOSProvisionerForTest(commander).verifyDockerBridgeNetworkWithInterval(testReadinessURL, 0)

	require.NoError(t, err)
	assert.Equal(t, []string{dockerNetworkVerifierImageCheck, check}, commander.calls)
}

func TestVerifyDockerBridgeNetworkRepairsOnce(t *testing.T) {
	check := dockerNetworkCheck(testReadinessURL)
	missing := make([]scriptedSSHResponse, 5)
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		dockerNetworkVerifierImageCheck:    {{}},
		check:                              appendMissingThenSuccess(missing),
		dockerNetworkDiagnosticsCmd:        {{}},
		"sudo systemctl daemon-reload":     {{}},
		"sudo systemctl -f restart docker": {{}},
		"sudo docker version":              {{}},
	}}

	err := newGoogleCOSProvisionerForTest(commander).verifyDockerBridgeNetworkWithInterval(testReadinessURL, 0)

	require.NoError(t, err)
	assert.Equal(t, 6, countCalls(commander.calls, check))
	assert.Equal(t, 1, countCalls(commander.calls, "sudo systemctl -f restart docker"))
}

func TestVerifyDockerBridgeNetworkFailsClosed(t *testing.T) {
	check := dockerNetworkCheck(testReadinessURL)
	missing := make([]scriptedSSHResponse, 15)
	for i := range missing {
		missing[i].err = errors.New("rules missing")
	}
	commander := &scriptedSSHCommander{responses: map[string][]scriptedSSHResponse{
		dockerNetworkVerifierImageCheck:                    {{}},
		check:                                              missing,
		dockerNetworkDiagnosticsCmd:                        {{}},
		"sudo systemctl daemon-reload":                     {{}},
		"sudo systemctl -f restart docker":                 {{}},
		"sudo docker version":                              {{}},
		"sudo systemctl stop docker.service docker.socket": {{}},
	}}

	err := newGoogleCOSProvisionerForTest(commander).verifyDockerBridgeNetworkWithInterval(testReadinessURL, 0)

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
