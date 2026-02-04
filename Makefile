## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

# Tool binaries
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen

# Tool versions
CONTROLLER_TOOLS_VERSION ?= v0.20.0
CRD_DIR ?= pullsecret/templates

.PHONY: manifests
manifests: controller-gen
	$(CONTROLLER_GEN) \
		crd:crdVersions=v1 \
		paths="./pkg/apis/pullsecret.pascal-kutscha.de/..." \
		output:crd:artifacts:config=$(CRD_DIR)

.PHONY: generate
generate: controller-gen
	$(CONTROLLER_GEN) object paths="./pkg/apis/pullsecret.pascal-kutscha.de/v1"

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN)
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen && $(LOCALBIN)/controller-gen --version | grep -q $(CONTROLLER_TOOLS_VERSION) || \
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

build-push:
	docker build -t pascalku/pullsecret-operator:latest .
	docker push pascalku/pullsecret-operator:latest