fmt:
	@test -z "$$(gofmt -s -l . 2>&1 | grep -v vendor/ | tee /dev/stderr)"

vet:
	@test -z "$$(go vet $(PKGS) 2>&1 | tee /dev/stderr)"

lint:
	$(if $(GOLINT), , \
		$(error Please install golint: go get -u golang.org/x/lint/golint))
	@test -z "$$($(GOLINT) ./... 2>&1 | grep -v vendor/ | grep -v "cli/" | grep -v "amazonec2/" |grep -v "openstack/" |grep -v "softlayer/" | grep -v "should have comment" | tee /dev/stderr)"
