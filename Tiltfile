IMAGE_NAME   = "db-operator"
RELEASE_NAME = "db-operator"
NAMESPACE    = "db-operator"

CHART_DIR  = "./charts/db-operator"

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

namespace_create(NAMESPACE)

k8s_yaml(
    helm(
        CHART_DIR,
        name      = RELEASE_NAME,
        namespace = NAMESPACE,
        set = [
            "image.repository={}".format(IMAGE_NAME),
            "image.tag=latest",
            "image.pullPolicy=Always",
            "instanceName=test",
        ],
    )
)

k8s_resource(
    RELEASE_NAME,
    port_forwards = ["8080:8080", "8081:8081"],
    labels        = ["db-operator"],
)

for suite, cmd in [
    ("test-migrations", "make integration-test-migrations"),
    ("test-postgres",   "make integration-test-postgres"),
    ("test-redis",      "make integration-test-redis"),
    ("test-nats",       "make integration-test-nats"),
]:
    local_resource(
        suite,
        cmd            = cmd,
        dir            = '.',
        resource_deps  = [RELEASE_NAME],
        labels         = ["db-operator"],
        allow_parallel = True,
    )
