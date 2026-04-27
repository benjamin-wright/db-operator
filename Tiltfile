IMAGE_NAME      = "db-operator"
MCP_IMAGE       = "db-mcp"
RELEASE_NAME    = "db-operator"
MCP_RELEASE     = "db-mcp"
NAMESPACE       = "db-operator"

CHART_DIR     = "./charts/db-operator"
MCP_CHART_DIR = "./charts/db-mcp"

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

k8s_yaml(
    helm(
        MCP_CHART_DIR,
        name      = MCP_RELEASE,
        namespace = NAMESPACE,
        set = [
            "image.repository={}".format(MCP_IMAGE),
            "image.tag=latest",
            "image.pullPolicy=Always",
        ],
    )
)

k8s_resource(
    RELEASE_NAME,
    port_forwards = ["8080:8080", "8081:8081"],
    labels        = ["db-operator"],
)

k8s_resource(
    MCP_RELEASE,
    port_forwards = ["8090:8080", "8091:8081"],
    labels        = ["db-mcp"],
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
