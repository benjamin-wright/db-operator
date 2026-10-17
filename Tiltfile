load('ext://helm_resource', 'helm_resource')
load('./tilt/tests.Tiltfile', 'validation')

allow_k8s_contexts('k3d-db-operator')
update_settings(max_parallel_updates=4, k8s_upsert_timeout_secs=300)

IMAGE_NAME      = "db-operator"
MCP_IMAGE       = "db-mcp"
RELEASE_NAME    = "db-operator"
NAMESPACE       = "db-operator"

CHART_DIR = "./charts/db-operator"

def namespace_create(name):
    """Emit a Kubernetes Namespace manifest so Tilt creates it if absent."""
    k8s_yaml(blob("""
apiVersion: v1
kind: Namespace
metadata:
  name: {}
""".format(name)))

docker_build(
    IMAGE_NAME,
    context    = '.',
    dockerfile = "./operator.Dockerfile",
    build_args = {"CMD_PATH": "./cmd"},
    only = [
        '.',
        "./operator.Dockerfile",
    ],
    ignore = ["**/bin/**", "**/cover.out", "**/*.test"],
)

docker_build(
    MCP_IMAGE,
    context    = '.',
    dockerfile = "./mcp.Dockerfile",
    only = [
        '.',
        "./mcp.Dockerfile",
    ],
    ignore = ["**/bin/**", "**/cover.out", "**/*.test"],
)

MIGRATIONS_IMAGE = "db-migrations"

docker_build(
    MIGRATIONS_IMAGE,
    context    = '.',
    dockerfile = "./migrations.Dockerfile",
    only = [
        '.',
        "./migrations.Dockerfile",
    ],
    ignore = ["**/bin/**", "**/cover.out", "**/*.test"],
    match_in_env_vars = True,
)

namespace_create(NAMESPACE)
namespace_create('cert-manager')

k8s_resource(
    new_name='namespaces',
    objects=['db-operator:Namespace', 'cert-manager:Namespace'],
    labels=['infrastructure'],
)

helm_resource(
    'cert-manager',
    namespace='cert-manager',
    chart='oci://quay.io/jetstack/charts/cert-manager:v1.17.2',
    flags=['--set=crds.enabled=true'],
    resource_deps=['namespaces'],
    labels=['infrastructure'],
)

local_resource(
    'cert-manager-ready',
    cmd=' && '.join([
        'kubectl rollout status deployment/{} -n cert-manager --timeout=180s'.format(name)
        for name in ['cert-manager', 'cert-manager-webhook', 'cert-manager-cainjector']
    ]),
    resource_deps=['cert-manager'],
    allow_parallel=True,
    labels=['infrastructure'],
)

# Persistent CRDs must never enter Tilt's delete/recreate update path.
local_resource(
    'db-operator-crds',
    cmd='kubectl apply -f charts/db-operator/crds && kubectl wait --for=condition=Established --timeout=120s ' + ' '.join([
        'crd/{}.db-operator.benjamin-wright.github.com'.format(kind)
        for kind in ['postgresdatabases', 'postgrescredentials', 'postgresmigrationsets', 'redisdatabases', 'rediscredentials', 'natsclusters', 'natsaccounts']
    ]),
    deps=['charts/db-operator/crds'],
    resource_deps=['generated-check'],
    allow_parallel=True,
    labels=['db-operator'],
)

k8s_yaml(
    helm(
        CHART_DIR,
        name      = RELEASE_NAME,
        namespace = NAMESPACE,
        skip_crds = True,
        set = [
            "image.repository={}".format(IMAGE_NAME),
            "image.tag=latest",
            "image.pullPolicy=Always",
            "instanceName=test",
            "migrationImage.repository={}".format(MIGRATIONS_IMAGE),
            "migrationImage.tag=latest",
            "migrationImage.pullPolicy=Always",
            "jobRegistryHost=db-operator-registry.localhost:5000",
            "mcp.enabled=true",
            "mcp.image.repository={}".format(MCP_IMAGE),
            "mcp.image.tag=latest",
            "mcp.image.pullPolicy=Always",
        ],
    )
)

k8s_resource(
    new_name='db-operator-admission',
    objects=[
        '{}-selfsigned:Issuer:{}'.format(RELEASE_NAME, NAMESPACE),
        '{}-webhook:Certificate:{}'.format(RELEASE_NAME, NAMESPACE),
        '{}-migrations:ValidatingWebhookConfiguration'.format(RELEASE_NAME),
    ],
    resource_deps=['cert-manager-ready', 'db-operator-crds'],
    labels=['db-operator'],
)

k8s_resource(
    RELEASE_NAME,
    port_forwards = ["8080:8080", "8081:8081"],
    labels        = ["db-operator"],
    resource_deps = ['db-operator-admission', 'db-operator-crds'],
    objects = [
        '{}:ServiceAccount:{}'.format(RELEASE_NAME, NAMESPACE),
        '{}-manager:ClusterRole'.format(RELEASE_NAME),
        '{}-manager:ClusterRoleBinding'.format(RELEASE_NAME),
        '{}-migration-claims:Role:{}'.format(RELEASE_NAME, NAMESPACE),
        '{}-migration-claims:RoleBinding:{}'.format(RELEASE_NAME, NAMESPACE),
    ],
)

k8s_resource(
    "{}-mcp".format(RELEASE_NAME),
    port_forwards = ["8090:8080", "8091:8081"],
    labels        = ["db-mcp"],
    resource_deps = ['db-operator-crds', 'namespaces'],
    objects = [
        '{}-mcp:ServiceAccount:{}'.format(RELEASE_NAME, NAMESPACE),
        '{}-mcp-manager:ClusterRole'.format(RELEASE_NAME),
        '{}-mcp-manager:ClusterRoleBinding'.format(RELEASE_NAME),
    ],
)

local_resource(
    'db-operator-ready',
    cmd=(
        'kubectl wait --for=condition=Ready certificate/{}-webhook -n {} --timeout=120s && '.format(RELEASE_NAME, NAMESPACE) +
        "kubectl wait --for=jsonpath='{.webhooks[0].clientConfig.caBundle}' " +
        'validatingwebhookconfiguration/{}-migrations --timeout=120s'.format(RELEASE_NAME)
    ),
    resource_deps=[RELEASE_NAME, 'db-operator-admission'],
    allow_parallel=True,
    labels=['db-operator'],
)

validation(RELEASE_NAME)
