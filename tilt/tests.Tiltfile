def validation(workload):
    source_deps = ['cmd', 'internal', 'pkg', 'go.mod', 'go.sum']
    watch_settings(ignore=['**/*.test'])
    local_resource(
        'format',
        cmd = 'make fmt',
        allow_parallel = True,
        auto_init = False,
        trigger_mode = TRIGGER_MODE_MANUAL,
        labels = ['validation'],
    )
    local_resource(
        'generate',
        cmd = 'make generate',
        auto_init = False,
        trigger_mode = TRIGGER_MODE_MANUAL,
        labels = ['validation'],
    )
    local_resource(
        'generated-check',
        cmd = 'make check-generated',
        deps = ['pkg/api', 'charts/db-operator/crds', 'Makefile', 'tools/check_generated.py'],
        allow_parallel = True,
        labels = ['validation'],
    )
    local_resource(
        'unit-tests',
        cmd = 'make test',
        deps = source_deps + ['Makefile'],
        resource_deps = ['generated-check'],
        allow_parallel = True,
        labels = ['validation'],
    )
    local_resource(
        'lint',
        cmd = 'make lint',
        deps = source_deps + ['charts/db-operator', 'Makefile', 'tools/check_format.py'],
        resource_deps = ['generated-check'],
        allow_parallel = True,
        labels = ['validation'],
    )
    local_resource(
        'integration-tests',
        cmd = 'make integration-test',
        deps = source_deps + ['Makefile'],
        resource_deps = [workload, 'db-operator-ready', 'unit-tests', 'lint'],
        allow_parallel = True,
        labels = ['validation'],
    )
    local_resource(
        'validation',
        cmd = 'echo "All local validation gates passed."',
        resource_deps = ['generated-check', 'unit-tests', 'lint', 'integration-tests'],
        labels = ['validation'],
    )
