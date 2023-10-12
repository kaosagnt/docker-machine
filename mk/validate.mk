fmt:
	@go mod download
	@test -z "$$(gofmt -s -l . 2>&1 | tee /dev/stderr)"

vet:
	@go mod download
	@test -z "$$(go vet $(PKGS) 2>&1 | tee /dev/stderr)"

lint:
	@go mod download
	@go install golang.org/x/lint/golint@latest
	@test -z "$$(golint ./... 2>&1 | grep -v "cli/" | grep -v "amazonec2/" |grep -v "openstack/" |grep -v "softlayer/" | grep -v "should have comment" | tee /dev/stderr)"
