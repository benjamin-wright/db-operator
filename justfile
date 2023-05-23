cluster_name := "db-operator"
registry_name := "db-operator-registry.localhost"
registry_port := "5000"
image_name := "db-operator-image"

start: create-cluster setup-context
stop: delete-cluster clear-context

create-cluster:
    #!/usr/bin/env bash
    set -euxo pipefail

    if ! k3d cluster list | grep -qw {{ cluster_name }}; then
        k3d cluster create {{ cluster_name }} \
            --registry-create {{ registry_name }}:0.0.0.0:{{ registry_port }} \
            --kubeconfig-update-default=false \
            --k3s-arg "--disable=traefik@server:*" \
            --wait;
    else
        echo "cluster {{ cluster_name }} already exists!"
    fi

setup-context:
    @mkdir -p .scratch
    @k3d kubeconfig get {{ cluster_name }} > .scratch/kubeconfig
    chmod og-r .scratch/kubeconfig

delete-cluster:
    if k3d cluster list | grep -qw {{ cluster_name }}; then \
        k3d cluster delete {{ cluster_name }}; \
    fi

clear-context:
    if [[ -f .scratch/kubeconfig ]]; then \
        rm .scratch/kubeconfig; \
    fi

crds:
    KUBECONFIG="$(pwd)/.scratch/kubeconfig" kubectl apply -f deploy/chart/crds

tilt:
    KUBECONFIG=.scratch/kubeconfig tilt up

test:
    go test --short -v ./...

int-test NAMESPACE:
    KUBECONFIG="$(pwd)/.scratch/kubeconfig" NAMESPACE="{{NAMESPACE}}" go test -run Integration -v ./...

build IMAGE_TAG:
    mkdir -p ./dist
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o ./dist/app ./cmd/operator/main.go
    docker build -t "{{IMAGE_TAG}}" -f deploy/Dockerfile ./dist

build-test IMAGE_TAG:
    mkdir -p ./dist
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o ./dist/tests ./tests
    docker build -t "{{IMAGE_TAG}}" -f deploy/Test.Dockerfile ./dist