#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

go test ./internal/hostedgenesis -run 'TestMicroVMLabCanaryHarnessExercisesLifecycleAndSecretChecks|TestMicroVMControllerRuntimeExercisesAppTheoryCommands|TestProvisionalDogfoodMicroVMClientUsesAppTheoryRegistryWithoutRawSDK' -count=1
