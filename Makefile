PROJECT_NAME := kube-balance
IMAGE_NAME := $(PROJECT_NAME)
TAG:= latest
REGISTRY:= docker.io/lokeshllkumar
CONTROLLER_IMAGE := $(REGISTRY)/$(IMAGE_NAME):$(TAG)

CRD_DIR := config/manager/crd/bases
RBAC_DIR := config/manager/rbac
MANAGER_DIR := config/manager
SAMPLES_DIR := config/samples

GO_BUILD_FLAGS := -ldflags="-s -w"

CONTROLLER_GEN := $(shell go env GOPATH)/bin/controller-gen

.PHONY: all build docker-build push deploy undeploy \
		generate install-crds uninstall-crds \
		install-profiles uninstall-profiles \
		test-apps delete-test-apps \
		annotate-node unannotate-node clean help \
		install-controller-gen

all: generate build docker-build

help:
	@echo "Commands":
	@echo "	make all 				- Runs generate, build and docker-build"
	@echo "	make generate 			- Generates CRD manifests and deepcopy code (requires controller-gen)"
	@echo " make build 				- Builds the Go binary for the controller"
	@echo " make docker-build		- Builds the Docker image for the controller"
	@echo " make push				- Pushes the Docker image to the configured container regsitry "
	@echo "	make install-crds"		- Installs the WorkloadProfile CRD into Kubernetes"
	@echo " make uninstall-crds		- Uninstalls the WorkloadProfile CRD from Kubernetes"
	@echo " make deploy				- Deploys the KubeBalance controller and RBAC to Kubernetes (includes push + install-creds)"
	@echo " make undeploy			- Removes the KubeBalance controller and RBAC from Kubernetes (includes uninstall-creds)"
	@echo " make install-profiles	- Deploys sample WorkloadProfile CRs"
	@echo "	make uninstall-profiles	- Removes sample WorkloadProfile CRs"
	@echo " make test-apps			- Deploys sample 'sensitive', 'noisy', and 'guaranteed' applications for testing"
	@echo " make delete-test-apps	- Removes the sample applications"
	@echo " make annotate-node		- Annotates a specified node as 'degraded' (for testing)"
	@echo " 						- Usage: make annotate-node NODE_NAME=<node-name>"
	@echo " make unannotate-node	- Removes the 'degraded' annotation from a specified node (for testing)"
	@echo " 						- Usage: make unannotate-node NODE_NAME=<node-name>"
	@echo " make clean				- Cleans up generated files and Docker images"
	@echo " make install-controller-gen - Installs the Go controller-gen tool"

# installing controller-gen
install-controller-gen:
	@echo "Installing controller-gen..."
	@go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest
	@echo "controller-gen installed at $(shell go env GOPATH)/bin/controller-gen"

# generating CRD manifests and Go deepcopy code
generate: install-controller-gen
	@echo "Generating CRD manifests and Go deepcopy code..."
	$(CONTROLLER_GEN) object paths="./api/v1alpha1/..."
	$(CONTROLLER_GEN) crd rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=$(CRD_DIR)
	@echo "Generation complete"

# buulding the Go binary
build:
	@echo "Building the Go binary..."
	go build $(GO_BUILD_FLAGS) -o manager cmd/manager/main.go
	@echo "Go binary build complete: manager"

# building the Docker image
docker-build:
	@echo "Building the Docker image..."
	docker build -t $(CONTROLLER_IMAGE) .
	@echo "Docker image build complete: $(CONTROLLER_IMAGE)"

# pushing the Docker image to the registry
push: docker-build
	@echo "Pushing Docker image to $(REGISTRY)"
	docker push $(CONTROLLER_IMAGE)
	@echo "Docker image published"

# installing CRDs
install-crds:
	@echo "Installing WorkloadProfile CRD..."
	kubectl apply -f $(CRD_DIR)/workloadprofiles.kube-balance.io.yaml
	@echo "WorkloadProfile CRD installed"

# waiting for CRDs to be established
wait-for-crds: install-crds
	@echo "Waiting for WorkloadProfile CRD to be established..."
	kubectl wait --for condition=Established crd/workloadprofiles.kube-balance.io --timeout=60s
	@echo "WorkloadProfile CRD is established."

# uninstalling CRDs
uninstall-crds:
	@echo "Uninstalling WorkloadProfile CRD..."
	kubectl delete -f $(CRD_DIR)/workloadprofiles.kube-balance.io.yaml
	@echo "WorkloadProfile CRD installed"

# deploying the controller and RBAC (push + install-crds)
deploy: push wait-for-crds
	@echo "Deploying KubeBalance controller and RBAC..."
	kubectl apply -k $(MANAGER_DIR)
	@echo "KubeBalance controller deployed"

# undeploying the controller and RBAC (uninstall-crds)
undeploy:
	@echo "Undeploying KubeBalance controller and RBAC..."
	kubectl delete -k $(MANAGER_DIR)
	@echo "KubeBalance controller undeployed"
	$(MAKE) uninstall-crds

# installing sample WorkloadProfile CRs
install-profiles:
	@echo "Installing sample WorkloadProfile CRs..."
	kubectl apply -f $(SAMPLES_DIR)/workloadprofile_cpu_intensive.yaml
	kubectl apply -f $(SAMPLES_DIR)/workloadprofile_io_intensive.yaml
	kubectl apply -f $(SAMPLES_DIR)/workloadprofile_batch_job.yaml
	kubectl apply -f $(SAMPLES_DIR)/workloadprofile_critical_service.yaml
	@echo "Sample WorkloadProfile CRs installed"

# uninstalling sample WorkloadProfile CRs
uninstall-profiles:
	@echo "Uninstalling sample WorkloadProfile CRs..."
	kubectl delete -f $(SAMPLES_DIR)/workloadprofile_cpu_intensive.yaml
	kubectl delete -f $(SAMPLES_DIR)/workloadprofile_io_intensive.yaml
	kubectl delete -f $(SAMPLES_DIR)/workloadprofile_batch_job.yaml
	kubectl delete -f $(SAMPLES_DIR)/workloadprofile_critical_service.yaml
	@echo "Sample WorkloadProfile CRs uninstalled"

# deploying sample applications
test-apps:
	@echo "Deploying sample applications..."
	kubectl apply -f $(SAMPLES_DIR)/deployment_sensitive_app.yaml
	kubectl apply -f $(SAMPLES_DIR)/deployment_noisy_app.yaml
	kubectl apply -f $(SAMPLES_DIR)/deployment_guaranteed_app.yaml
	@echo "Sample applications deployed"

# deleting sample applications
delete-test-apps:
	@echo "Deleting sample applications..."
	kubectl delete -f $(SAMPLES_DIR)/deployment_sensitive_app.yaml
	kubectl delete -f $(SAMPLES_DIR)/deployment_noisy_app.yaml
	kubectl delete -f $(SAMPLES_DIR)/deployment_guaranteed_app.yaml
	@echo "Sample applications deleted"

# annotating a node as degraded for testing
annotate-node:
ifndef NODE_NAME
	$(error NODE_NAME is required. Usage: make annotate-node NODE_NAME=<node-name>)
endif
	@echo "Annotating node $(NODE_NAME) as degraded..."
	kubectl annotate node $(NODE_NAME) kube-balance.io/degraded-io="true" --overwrite
	@echo "Node $(NODE_NAME) annotated"

# unannotating a node
unannotate-node:
ifndef NODE_NAME
	$(error NODE_NAME is required. Usage: make unannotate-node NODE_NAME=<node-name>)
endif
	@echo "Removing degraded annotation from node $(NODE_NAME)..."
	kubectl annotate node $(NODE_NAME) kube-balance.io/degraded-io-
	@echo "Node $(NODE_NAME) unannotated"

# cleaning up build artifacts
clean:
	@echo "Cleaning up..."
	rm -f manager
	@echo "Cleaned"
