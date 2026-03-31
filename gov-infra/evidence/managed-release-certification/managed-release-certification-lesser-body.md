# lesser-body managed certification

- Generated at: `2026-03-30T16:45:00Z`
- Base URL: `https://lab.lesser.host`
- Instance slug: `simulacrum`
- Lesser version: `v1.2.6`
- lesser-body version: `v0.2.3`
- Overall status: `pass`

## Checks

- `lesser_body_version_selected`: `pass` - requested lesser-body release v0.2.3 will be validated for managed certification
- `lesser_body_compatibility_contract_valid`: `pass` - requested lesser-body release matches the published lesser-host managed compatibility contract
- `lesser_body_template_preflight_valid`: `pass` - published template lesser-body-managed-dev.template.json passed lesser-host managed body template preflight
- `lesser_body_template_changeset_valid`: `pass` - published template lesser-body-managed-dev.template.json passed cloudformation_deploy_no_execute_changeset and is recorded at managed/updates/simulacrum/job-update-1/body-template-certification.json
- `lesser_body_completed`: `pass` - lesser-body managed deploy completed
- `lesser_body_runner_visibility_present`: `pass` - https://console.aws.amazon.com/codebuild/home?#/builds/job-update-1-body/view/new
- `lesser_body_receipt_key_defined`: `pass` - managed/updates/simulacrum/job-update-1/body-state.json

## Job

- `lesser-body` `job-update-1`: status=`ok` step=`done` version=`v0.2.3` receipt=`managed/updates/simulacrum/job-update-1/body-state.json`
  run_url: https://console.aws.amazon.com/codebuild/home?#/builds/job-update-1-body/view/new
  body_run_url: https://console.aws.amazon.com/codebuild/home?#/builds/job-update-1-body/view/new
  template_path: lesser-body-managed-dev.template.json
  template_certification_key: managed/updates/simulacrum/job-update-1/body-template-certification.json
  template_verification_mode: cloudformation_deploy_no_execute_changeset
  note: lesser-body updated
