#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

go test ./internal/hostedgenesis -run 'TestMicroVMLabCanaryHarnessExercisesM16LifecycleAndSecretChecks|TestMicroVMControllerRuntimeExercisesAppTheoryM16Commands|TestMicroVMControllerRuntimeUsesAppTheoryM16WithoutLocalAdapter' -count=1
go test ./cmd/hosted-genesis-microvm-controller -run 'TestControllerAppRegistersAppTheoryM16Routes|TestControllerAuthHookRequiresHashedBearerAndTenantHeaders|TestControllerEventFailsClosedWhenDisabled' -count=1
