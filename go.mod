module github.com/docker/machine

go 1.23.0

replace github.com/dgrijalva/jwt-go => github.com/golang-jwt/jwt/v4 v4.5.1

require (
	github.com/Azure/azure-sdk-for-go v5.0.0-beta+incompatible
	github.com/Azure/go-autorest v7.2.1+incompatible
	github.com/aws/aws-sdk-go v1.36.21
	github.com/bugsnag/bugsnag-go v1.0.6-0.20151120182711-02e952891c52
	github.com/cenkalti/backoff v0.0.0-20141124221459-9831e1e25c87
	github.com/codegangsta/cli v1.11.1-0.20151120215642-0302d3914d2a
	github.com/digitalocean/godo v1.0.1-0.20170317202744-d59ed2fe842b
	github.com/docker/docker v25.0.6+incompatible
	github.com/exoscale/egoscale v0.9.23
	github.com/intel-go/cpuid v0.0.0-20181003105527-1a4a6f06a1c6
	github.com/rackspace/gophercloud v1.0.1-0.20150408191457-ce0f487f6747
	github.com/skarademir/naturalsort v0.0.0-20150715044055-69a5d87bef62
	github.com/stretchr/testify v1.9.0
	github.com/vmware/govcloudair v0.0.2
	github.com/vmware/govmomi v0.6.2
	golang.org/x/crypto v0.36.0
	golang.org/x/net v0.37.0
	golang.org/x/oauth2 v0.18.0
	golang.org/x/sys v0.31.0
	google.golang.org/api v0.169.0
)

require (
	github.com/containerd/log v0.1.0 // indirect
	github.com/distribution/reference v0.6.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/golang-jwt/jwt/v4 v4.5.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.49.0 // indirect
	go.opentelemetry.io/otel v1.28.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.28.0 // indirect
	go.opentelemetry.io/otel/metric v1.28.0 // indirect
	go.opentelemetry.io/otel/sdk v1.28.0 // indirect
	go.opentelemetry.io/otel/trace v1.28.0 // indirect
	golang.org/x/sync v0.12.0 // indirect
)

require (
	cloud.google.com/go/compute v1.25.1 // indirect
	cloud.google.com/go/compute/metadata v0.2.3 // indirect
	github.com/Microsoft/go-winio v0.6.1 // indirect
	github.com/bitly/go-simplejson v0.5.1 // indirect
	github.com/bugsnag/osext v0.0.0-20130617224835-0dd3f918b21b // indirect
	github.com/bugsnag/panicwrap v0.0.0-20160118154447-aceac81c6e2f // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dgrijalva/jwt-go v3.2.0+incompatible // indirect
	github.com/docker/go-connections v0.4.0
	github.com/docker/go-units v0.2.1-0.20151230175859-0bbddae09c5a // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/go-querystring v0.0.0-20140804062624-30f7a39f4a21 // indirect
	github.com/google/s2a-go v0.1.7 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.2 // indirect
	github.com/googleapis/gax-go/v2 v2.12.2 // indirect
	github.com/jinzhu/copier v0.0.0-20180308034124-7e38e58719c3 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/juju/loggo v1.0.0 // indirect
	github.com/mitchellh/mapstructure v0.0.0-20140721150620-740c764bc614 // indirect
	github.com/moby/term v0.5.0 // indirect
	github.com/morikuni/aec v1.0.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.0.2
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/tent/http-link-go v0.0.0-20130702225549-ac974c61c2f9 // indirect
	go.opencensus.io v0.24.0 // indirect
	golang.org/x/mod v0.17.0 // indirect
	golang.org/x/term v0.30.0
	golang.org/x/text v0.23.0 // indirect
	golang.org/x/tools v0.21.1-0.20240508182429-e35e4ccd0d2d // indirect
	google.golang.org/appengine v1.6.8 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20240701130421-f6361c86f094 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240701130421-f6361c86f094 // indirect
	google.golang.org/grpc v1.64.1 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
	gopkg.in/check.v1 v1.0.0-20160105164936-4f90aeace3a2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	gotest.tools/v3 v3.5.1 // indirect
	launchpad.net/gocheck v0.0.0-20140225173054-000000000087 // indirect
)
