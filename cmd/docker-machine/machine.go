package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"path/filepath"

	"github.com/codegangsta/cli"
	"github.com/docker/machine/commands"
	"github.com/docker/machine/commands/mcndirs"
	"github.com/docker/machine/drivers/amazonec2"
	"github.com/docker/machine/drivers/azure"
	"github.com/docker/machine/drivers/digitalocean"
	"github.com/docker/machine/drivers/exoscale"
	"github.com/docker/machine/drivers/generic"
	"github.com/docker/machine/drivers/google"
	"github.com/docker/machine/drivers/hyperv"
	"github.com/docker/machine/drivers/none"
	"github.com/docker/machine/drivers/openstack"
	"github.com/docker/machine/drivers/rackspace"
	"github.com/docker/machine/drivers/softlayer"
	"github.com/docker/machine/drivers/virtualbox"
	"github.com/docker/machine/drivers/vmwarefusion"
	"github.com/docker/machine/drivers/vmwarevcloudair"
	"github.com/docker/machine/drivers/vmwarevsphere"
	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/drivers/inprocess"
	"github.com/docker/machine/libmachine/drivers/plugin"
	"github.com/docker/machine/libmachine/drivers/plugin/localbinary"
	"github.com/docker/machine/libmachine/log"
	"github.com/docker/machine/version"
)

var AppHelpTemplate = `Usage: {{.Name}} {{if .Flags}}[OPTIONS] {{end}}COMMAND [arg...]

{{.Usage}}

Version: {{.Version}}{{if or .Author .Email}}

Author:{{if .Author}}
  {{.Author}}{{if .Email}} - <{{.Email}}>{{end}}{{else}}
  {{.Email}}{{end}}{{end}}
{{if .Flags}}
Options:
  {{range .Flags}}{{.}}
  {{end}}{{end}}
Commands:
  {{range .Commands}}{{.Name}}{{with .ShortName}}, {{.}}{{end}}{{ "\t" }}{{.Usage}}
  {{end}}
Run '{{.Name}} COMMAND --help' for more information on a command.
`

var CommandHelpTemplate = `Usage: docker-machine {{.Name}}{{if .Flags}} [OPTIONS]{{end}} [arg...]

{{.Usage}}{{if .Description}}

Description:
   {{.Description}}{{end}}{{if .Flags}}

Options:
   {{range .Flags}}
   {{.}}{{end}}{{ end }}
`

// Runs before cli parsing, like setDebugOutputLevel, because the version line is logged before app.Run.
func setLogFormat() {
	format := os.Getenv("MACHINE_LOG_FORMAT")
	for i, f := range os.Args {
		if strings.HasPrefix(f, "--log-format=") {
			format = strings.TrimPrefix(f, "--log-format=")
		} else if f == "--log-format" && i+1 < len(os.Args) {
			format = os.Args[i+1]
		}
	}

	if err := log.SetFormat(format); err != nil {
		fmt.Fprintf(os.Stderr, "Error setting log format: %s\n", err)
		os.Exit(1)
	}
}

func setDebugOutputLevel() {
	// TODO: I'm not really a fan of this method and really would rather
	// use -v / --verbose TBQH
	for _, f := range os.Args {
		if f == "-D" || f == "--debug" || f == "-debug" {
			log.SetDebug(true)
		}
	}

	debugEnv := os.Getenv("MACHINE_DEBUG")
	if debugEnv != "" {
		showDebug, err := strconv.ParseBool(debugEnv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing boolean value from MACHINE_DEBUG: %s\n", err)
			os.Exit(1)
		}
		log.SetDebug(showDebug)
	}
}

func main() {
	if os.Getenv(localbinary.PluginEnvKey) == localbinary.PluginEnvVal {
		if err := log.SetFormat(os.Getenv(localbinary.PluginEnvLogFormat)); err != nil {
			fmt.Fprintf(os.Stderr, "Error setting log format: %s\n", err)
			os.Exit(1)
		}
		driverName := os.Getenv(localbinary.PluginEnvDriverName)
		runDriver(driverName)
		return
	}

	localbinary.CurrentBinaryIsDockerMachine = true
	for name, newDriver := range coreDrivers {
		inprocess.Register(name, newDriver)
	}

	setLogFormat()
	setDebugOutputLevel()
	cli.AppHelpTemplate = AppHelpTemplate
	cli.CommandHelpTemplate = CommandHelpTemplate
	app := cli.NewApp()
	app.Name = filepath.Base(os.Args[0])
	app.Author = "Docker Machine Contributors"
	app.Email = "https://github.com/docker/machine"

	app.Commands = commands.Commands
	app.CommandNotFound = cmdNotFound
	app.Usage = "Create and manage machines running Docker."
	app.Version = version.FullVersion()

	log.Debug("Docker Machine Version: ", app.Version)

	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "debug, D",
			Usage: "Enable debug mode",
		},
		cli.StringFlag{
			EnvVar: "MACHINE_STORAGE_PATH",
			Name:   "storage-path, s",
			Value:  mcndirs.GetBaseDir(),
			Usage:  "Configures storage path",
		},
		cli.StringFlag{
			EnvVar: "MACHINE_TLS_CA_CERT",
			Name:   "tls-ca-cert",
			Usage:  "CA to verify remotes against",
			Value:  "",
		},
		cli.StringFlag{
			EnvVar: "MACHINE_TLS_CA_KEY",
			Name:   "tls-ca-key",
			Usage:  "Private key to generate certificates",
			Value:  "",
		},
		cli.StringFlag{
			EnvVar: "MACHINE_TLS_CLIENT_CERT",
			Name:   "tls-client-cert",
			Usage:  "Client cert to use for TLS",
			Value:  "",
		},
		cli.StringFlag{
			EnvVar: "MACHINE_TLS_CLIENT_KEY",
			Name:   "tls-client-key",
			Usage:  "Private key used in client TLS auth",
			Value:  "",
		},
		cli.StringFlag{
			EnvVar: "MACHINE_GITHUB_API_TOKEN",
			Name:   "github-api-token",
			Usage:  "Token to use for requests to the Github API",
			Value:  "",
		},
		cli.BoolFlag{
			EnvVar: "MACHINE_NATIVE_SSH",
			Name:   "native-ssh",
			Usage:  "Use the native (Go-based) SSH implementation.",
		},
		cli.StringFlag{
			EnvVar: "MACHINE_BUGSNAG_API_TOKEN",
			Name:   "bugsnag-api-token",
			Usage:  "BugSnag API token for crash reporting",
			Value:  "",
		},
		cli.StringFlag{
			EnvVar: "MACHINE_LOG_FORMAT",
			Name:   "log-format",
			Usage:  "Log output format (text or json)",
			Value:  log.FormatText,
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Error(err)
	}
}

var coreDrivers = map[string]func() drivers.Driver{
	"amazonec2":       func() drivers.Driver { return amazonec2.NewDriver("", "") },
	"azure":           func() drivers.Driver { return azure.NewDriver("", "") },
	"digitalocean":    func() drivers.Driver { return digitalocean.NewDriver("", "") },
	"exoscale":        func() drivers.Driver { return exoscale.NewDriver("", "") },
	"generic":         func() drivers.Driver { return generic.NewDriver("", "") },
	"google":          func() drivers.Driver { return google.NewDriver("", "") },
	"hyperv":          func() drivers.Driver { return hyperv.NewDriver("", "") },
	"none":            func() drivers.Driver { return none.NewDriver("", "") },
	"openstack":       func() drivers.Driver { return openstack.NewDriver("", "") },
	"rackspace":       func() drivers.Driver { return rackspace.NewDriver("", "") },
	"softlayer":       func() drivers.Driver { return softlayer.NewDriver("", "") },
	"virtualbox":      func() drivers.Driver { return virtualbox.NewDriver("", "") },
	"vmwarefusion":    func() drivers.Driver { return vmwarefusion.NewDriver("", "") },
	"vmwarevcloudair": func() drivers.Driver { return vmwarevcloudair.NewDriver("", "") },
	"vmwarevsphere":   func() drivers.Driver { return vmwarevsphere.NewDriver("", "") },
}

func runDriver(driverName string) {
	newDriver, ok := coreDrivers[driverName]
	if !ok {
		fmt.Fprintf(os.Stderr, "Unsupported driver: %s\n", driverName)
		os.Exit(1)
	}
	plugin.RegisterDriver(newDriver())
}

func cmdNotFound(c *cli.Context, command string) {
	log.Errorf(
		"%s: '%s' is not a %s command. See '%s --help'.",
		c.App.Name,
		command,
		c.App.Name,
		os.Args[0],
	)
	os.Exit(1)
}
