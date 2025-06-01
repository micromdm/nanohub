VERSION = $(shell git describe --tags --always --dirty)
LDFLAGS=-ldflags "-X main.version=$(VERSION)"
OSARCH=$(shell go env GOHOSTOS)-$(shell go env GOHOSTARCH)

NANOHUB=\
	nanohub-darwin-amd64 \
	nanohub-darwin-arm64 \
	nanohub-linux-amd64 \
	nanohub-linux-arm64 \
	nanohub-linux-arm \
	nanohub-windows-amd64.exe

my: nanohub-$(OSARCH)

$(NANOHUB): cmd/nanohub
	GOOS=$(word 2,$(subst -, ,$@)) GOARCH=$(word 3,$(subst -, ,$(subst .exe,,$@))) go build $(LDFLAGS) -o $@ ./$<

nanohub-%-$(VERSION).zip: nanohub-%.exe
	rm -rf $(subst .zip,,$@)
	mkdir $(subst .zip,,$@)
	ln $^ $(subst .zip,,$@)
	zip -r $@ $(subst .zip,,$@)
	rm -rf $(subst .zip,,$@)

nanohub-%-$(VERSION).zip: nanohub-%
	rm -rf $(subst .zip,,$@)
	mkdir $(subst .zip,,$@)
	ln $^ $(subst .zip,,$@)
	zip -r $@ $(subst .zip,,$@)
	rm -rf $(subst .zip,,$@)

clean:
	rm -rf nanohub-*

release: $(foreach bin,$(NANOHUB),$(subst .exe,,$(bin))-$(VERSION).zip)

test:
	go test -v -cover -race ./...

.PHONY: my $(NANOHUB) clean release test
