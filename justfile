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
    KUBECONFIG=".scratch/kubeconfig" kubectl apply -f deploy/chart/crds

run:
    KUBECONFIG=".scratch/kubeconfig" NAMESPACE=default go run .

test:
    KUBECONFIG=".scratch/kubeconfig" NAMESPACE=default go test -v ./...

image:
    docker build -t "{{ image_name }}" -f deploy/Dockerfile .