NAMESPACE     := jsell-agent-boss
IMAGE_NAME    := boss-coordinator
REGISTRY      := default-route-openshift-image-registry.apps.okd1.timslab
IMAGE_TAG     := latest
IMAGE         := $(REGISTRY)/$(NAMESPACE)/$(IMAGE_NAME):$(IMAGE_TAG)

.PHONY: build install build-image push-image deploy rollout

build:
	cd frontend && npm install && npm run build
	CGO_ENABLED=0 go build -o boss ./cmd/boss/

install:
	cd frontend && npm install && npm run build
	CGO_ENABLED=0 go install ./cmd/boss/

build-image:
	podman build -t $(IMAGE) -f deploy/Dockerfile .

push-image:
	podman push $(IMAGE) --tls-verify=false

deploy:
	oc apply -f deploy/openshift/namespace.yaml
	oc process -f deploy/openshift/postgresql-credentials.yaml | oc apply -f -
	oc apply -f deploy/openshift/configmap.yaml
	oc apply -f deploy/openshift/postgresql.yaml
	oc apply -f deploy/openshift/deployment.yaml
	oc apply -f deploy/openshift/service.yaml
	oc apply -f deploy/openshift/route.yaml

rollout: build-image push-image
	oc rollout restart deploy/boss-coordinator -n $(NAMESPACE)
