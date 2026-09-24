package provision

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/docker/machine/libmachine/auth"
	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/engine"
	"github.com/docker/machine/libmachine/log"
	"github.com/docker/machine/libmachine/mcnutils"
	"github.com/docker/machine/libmachine/provision/pkgaction"
	"github.com/docker/machine/libmachine/provision/serviceaction"
	"github.com/docker/machine/libmachine/swarm"
)

func init() {
	Register("GoogleContainerOptimizedOS", &RegisteredProvisioner{
		New: NewGoogleCOSProvisioner,
	})
}

func NewGoogleCOSProvisioner(d drivers.Driver) Provisioner {
	return &GoogleCOSProvisioner{
		NewSystemdProvisioner("cos", d),
	}
}

type GoogleCOSProvisioner struct {
	SystemdProvisioner
}

const (
	readinessGateMetadataKey = "gitlab-docker-network-readiness-gate"
	readinessURLMetadataKey  = "gitlab-docker-network-readiness-url"
)

func metadataAttributeCheck(key string) string {
	return `
url=http://169.254.169.254/computeMetadata/v1/instance/attributes/` + key + `
for attempt in 1 2 3; do
	code=$(curl -s --max-time 3 -o /tmp/gitlab-readiness-metadata -w '%{http_code}' -H 'Metadata-Flavor: Google' "$url") || {
		sleep 1
		continue
	}
	case "$code" in
		404) exit 0 ;;
		200) cat /tmp/gitlab-readiness-metadata; exit 0 ;;
		403) echo 'GCE readiness metadata request was forbidden (HTTP 403)' >&2; exit 1 ;;
	esac
	sleep 1
done
echo 'GCE readiness metadata unavailable after 3 attempts' >&2
exit 1`
}

const (
	cloudInitResultFile  = "/run/cloud-init/result.json"
	cloudInitWaitTimeout = 5 * time.Minute
)

// cloudInitWaitCmd polls for result.json, which cloud-final writes after every module
// including runcmd has run, and only then asks `cloud-init status` for the errors.
//
// `cloud-init status --wait` is not usable on stock COS: cloud-init 24.4 reports
// "Failed due to systemd unit failure" (exit 1) while it is still running because COS
// pulls the cloud-init services in through cloud-init.target and leaves them
// UnitFileState=disabled, and once done it exits 2 ("degraded done") because
// DataSourceGCELocal fails without a DHCP client. Neither is a failed boot, so 2 is
// treated as done after the file exists; 1 (errors in result.json) and 124 (timeout)
// fail the create.
func cloudInitWaitCmd(resultFile string, timeout time.Duration) string {
	return fmt.Sprintf(`sudo sh -c '
deadline=$(( $(date +%%s) + %d ))
until [ -f %s ]; do
	if [ "$(date +%%s)" -ge "$deadline" ]; then
		echo "timed out after %s waiting for cloud-init to finish" >&2
		cloud-init status --long
		exit 124
	fi
	sleep 2
done
cloud-init status --long
rc=$?
[ "$rc" -eq 2 ] && exit 0
exit $rc
'`, int(timeout.Seconds()), resultFile, timeout)
}

const dockerNetworkVerifierImageCheck = `sudo docker image inspect alpine:latest >/dev/null`

const dockerNetworkProbeContainer = "gitlab-docker-network-readiness-probe"

func dockerNetworkCheck(url string) string {
	return `sudo sh -c '
docker rm -f ` + dockerNetworkProbeContainer + ` >/dev/null 2>&1 || true
timeout 15 docker run \
	--name ` + dockerNetworkProbeContainer + ` \
	--rm \
	--pull=never \
	--network bridge \
	alpine:latest \
	wget -S -q -O /dev/null -T 8 "$1" 2>&1 | grep -q "HTTP/"
rc=$?
docker rm -f ` + dockerNetworkProbeContainer + ` >/dev/null 2>&1 || true
exit $rc
' probe ` + shellQuote(url) + ``
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// dockerNetworkDiagnosticsCmd is best-effort evidence collected before the
// single repair restart. It intentionally continues when an individual probe
// fails so the provisioning log contains as much state as possible.
const dockerNetworkDiagnosticsCmd = `sudo sh -c '
echo "docker-network-readiness: container bridge egress unavailable"
systemctl show docker.service iptables-restore.service gpu-driver.service \
	-p Id -p ActiveEnterTimestamp -p ExecMainStartTimestamp
docker network inspect bridge
iptables -t nat -S POSTROUTING
iptables -S FORWARD
'`

func (p *GoogleCOSProvisioner) String() string {
	return "cos"
}

func (p *GoogleCOSProvisioner) Package(_ string, _ pkgaction.PackageAction) error {
	return nil
}

func (p *GoogleCOSProvisioner) GenerateDockerOptions(dockerPort int) (*DockerOptions, error) {
	var (
		engineCfg bytes.Buffer
	)

	driverNameLabel := fmt.Sprintf("provider=%s", p.Driver.DriverName())
	p.EngineOptions.Labels = append(p.EngineOptions.Labels, driverNameLabel)

	engineConfigTmpl := `[Service]
ExecStart=
ExecStart=/usr/bin/dockerd -H tcp://0.0.0.0:{{.DockerPort}} -H unix:///var/run/docker.sock --tlsverify --tlscacert {{.AuthOptions.CaCertRemotePath}} --tlscert {{.AuthOptions.ServerCertRemotePath}} --tlskey {{.AuthOptions.ServerKeyRemotePath}} {{ range .EngineOptions.Labels }}--label {{.}} {{ end }}{{ range .EngineOptions.InsecureRegistry }}--insecure-registry {{.}} {{ end }}{{ range .EngineOptions.RegistryMirror }}--registry-mirror {{.}} {{ end }}{{ range .EngineOptions.ArbitraryFlags }}--{{.}} {{ end }}
Environment={{range .EngineOptions.Env}}{{ printf "%q" . }} {{end}}
`
	t, err := template.New("engineConfig").Parse(engineConfigTmpl)
	if err != nil {
		return nil, err
	}

	engineConfigContext := EngineConfigContext{
		DockerPort:    dockerPort,
		AuthOptions:   p.AuthOptions,
		EngineOptions: p.EngineOptions,
	}

	_ = t.Execute(&engineCfg, engineConfigContext)

	return &DockerOptions{
		EngineOptions:     engineCfg.String(),
		EngineOptionsPath: p.DaemonOptionsFile,
	}, nil
}

func (p *GoogleCOSProvisioner) Provision(swarmOptions swarm.Options, authOptions auth.Options, engineOptions engine.Options) error {
	p.SwarmOptions = swarmOptions
	p.AuthOptions = authOptions
	p.EngineOptions = engineOptions
	swarmOptions.Env = engineOptions.Env

	readinessEnabled, err := p.readinessEnabled()
	if err != nil {
		return fmt.Errorf("determining whether the Google COS readiness gate is enabled: %w", err)
	}
	readinessURL, err := p.readinessURL()
	if err != nil {
		return fmt.Errorf("determining the Google COS readiness URL: %w", err)
	}
	if readinessEnabled {
		log.Info("Waiting for cloud-init to finish before provisioning Docker")
		if err := p.waitForCloudInit(); err != nil {
			return err
		}
	}

	log.Debug("Setting hostname")
	err = p.SetHostname(p.Driver.GetMachineName())
	if err != nil {
		return err
	}

	log.Debug("Waiting for Docker Daemon")
	err = mcnutils.WaitFor(p.dockerDaemonResponding)
	if err != nil {
		return err
	}

	if err := setupRemoteAuthOptions(p); err != nil {
		return err
	}

	log.Debug("Configuring auth")
	err = ConfigureAuth(p)
	if err != nil {
		return err
	}

	log.Debug("Configuring local firewall")
	err = p.configureLocalFirewall()
	if err != nil {
		return err
	}

	log.Debug("Configuring swarm")
	err = configureSwarm(p, swarmOptions, p.AuthOptions)
	if err != nil {
		return err
	}

	log.Debug("Enabling Docker in systemd")
	if err := p.Service("docker", serviceaction.Enable); err != nil {
		return err
	}

	if readinessURL != "" {
		if err := p.verifyDockerBridgeNetwork(readinessURL); err != nil {
			return err
		}
	}

	return nil
}

func (p *GoogleCOSProvisioner) readinessEnabled() (bool, error) {
	out, err := p.SSHCommand(metadataAttributeCheck(readinessGateMetadataKey))
	if err != nil {
		return false, fmt.Errorf("checking Google COS readiness metadata: %w", err)
	}

	return strings.TrimSpace(out) == "true", nil
}

func (p *GoogleCOSProvisioner) readinessURL() (string, error) {
	out, err := p.SSHCommand(metadataAttributeCheck(readinessURLMetadataKey))
	if err != nil {
		return "", fmt.Errorf("checking Google COS readiness URL metadata: %w", err)
	}

	return strings.TrimSpace(out), nil
}

func (p *GoogleCOSProvisioner) waitForCloudInit() error {
	out, err := p.SSHCommand(cloudInitWaitCmd(cloudInitResultFile, cloudInitWaitTimeout))
	if err != nil {
		if out != "" {
			log.Debugf("cloud-init status output:\n%s", out)
		}
		return fmt.Errorf("waiting for cloud-init readiness gate: %w", err)
	}

	if strings.Contains(out, "degraded") {
		log.Warnf("cloud-init finished with recoverable errors:\n%s", out)
	} else if out != "" {
		log.Debugf("cloud-init status output:\n%s", out)
	}

	return nil
}

func (p *GoogleCOSProvisioner) verifyDockerBridgeNetwork(url string) error {
	return p.verifyDockerBridgeNetworkWithInterval(url, time.Second)
}

func (p *GoogleCOSProvisioner) verifyDockerBridgeNetworkWithInterval(url string, interval time.Duration) error {
	if _, err := p.SSHCommand(dockerNetworkVerifierImageCheck); err != nil {
		return fmt.Errorf("Docker network verifier image alpine:latest is not present; refusing to run a registry-dependent readiness check: %w", err)
	}

	if p.waitForDockerNetwork(url, 5, interval) {
		return nil
	}

	out, diagErr := p.SSHCommand(dockerNetworkDiagnosticsCmd)
	if out != "" {
		log.Warnf("Docker bridge network diagnostics before repair:\n%s", out)
	}
	if diagErr != nil {
		log.Warnf("Collecting Docker bridge network diagnostics returned: %v", diagErr)
	}

	log.Warn("Docker bridge network readiness check failed; restarting Docker once")
	if err := p.Service("docker", serviceaction.Restart); err != nil {
		p.stopDocker()
		return fmt.Errorf("restarting Docker after bridge network readiness failure: %w", err)
	}
	if err := mcnutils.WaitFor(p.dockerDaemonResponding); err != nil {
		p.stopDocker()
		return fmt.Errorf("waiting for Docker after bridge network repair restart: %w", err)
	}
	if !p.waitForDockerNetwork(url, 10, interval) {
		p.stopDocker()
		return errors.New("Docker bridge network remained unavailable after one restart")
	}
	log.Info("Docker bridge network recovered after one repair restart")

	return nil
}

func (p *GoogleCOSProvisioner) waitForDockerNetwork(url string, attempts int, interval time.Duration) bool {
	check := dockerNetworkCheck(url)
	return mcnutils.WaitForSpecific(func() bool {
		_, err := p.SSHCommand(check)
		return err == nil
	}, attempts, interval) == nil
}

func (p *GoogleCOSProvisioner) stopDocker() {
	log.Warn("Stopping Docker as part of bridge network readiness fail-closed cleanup")
	if _, err := p.SSHCommand("sudo systemctl stop docker.service docker.socket"); err != nil {
		log.Warnf("Failed to stop Docker during cleanup: %v", err)
	}
}

func (p *GoogleCOSProvisioner) dockerDaemonResponding() bool {
	log.Debug("Checking Docker Daemon")

	out, err := p.SSHCommand("sudo docker version")
	if err != nil {
		log.Warnf("Error getting SSH command to check if the daemon is up: %s", err)
		log.Debugf("'sudo docker version' output:\n%s", out)
		return false
	}

	return true
}

func (p *GoogleCOSProvisioner) configureLocalFirewall() error {
	out, err := p.SSHCommand("sudo iptables -A INPUT -p tcp -m tcp --dport 2376 -j ACCEPT")
	if err != nil {
		log.Debugf("Iptables output:\n%s", out)
		return err
	}

	return nil
}
