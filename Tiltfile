allow_k8s_contexts(['db-operator'])
load('ext://namespace', 'namespace_yaml')

local_resource(
    'unit-test',
    'just test',
    auto_init = False,
    trigger_mode = TRIGGER_MODE_MANUAL,
    labels=['tests']
)

custom_build(
    'db-operator',
    'just build $EXPECTED_REF',
    [
        'cmd/operator',
        'deploy/Dockerfile',
        'internal',
        'pkg',
        'go.mod',
        'go.sum',
    ],
    ignore = [
        'dist/*',
        '**/*_test.go'
    ]
)

custom_build(
    'db-operator-tests',
    'just build-test $EXPECTED_REF',
    [
        'cmd/operator',
        'deploy/Dockerfile',
        'internal',
        'tests',
        'pkg',
        'go.mod',
        'go.sum',
    ],
    ignore = [
        'dist/*',
    ]
)

k8s_yaml(namespace_yaml('test-ns'))
k8s_yaml(helm(
    'deploy/chart',
    name='db-operator',
    namespace='test-ns',
    set=[
        'image=db-operator'
    ]
))

k8s_resource(
    'db-operator',
    labels=["operator"],
    trigger_mode=TRIGGER_MODE_MANUAL,
)

k8s_yaml(helm(
    'deploy/test-chart',
    name='db-operator-tests',
    namespace='test-ns',
    set=[
        'image=db-operator-tests'
    ]
))

k8s_resource(
    'db-operator-test',
    labels=["tests"],
    trigger_mode=TRIGGER_MODE_MANUAL,
    auto_init=False
)