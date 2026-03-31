# Managed release certification

- Generated at: `2026-03-30T16:45:00Z`
- Base URL: `https://lab.lesser.host`
- Instance slug: `simulacrum`
- Lesser version: `v1.2.6`
- lesser-body version: `v0.2.3`
- Require lesser-body: `true`
- Require MCP: `true`
- Overall status: `pass`

## Checks

- `compatibility_contract_valid`: `pass` - requested release matches the lesser-host managed compatibility contract
- `lesser_body_version_selected`: `pass` - requested lesser-body release v0.2.3 will be validated for managed certification
- `lesser_body_compatibility_contract_valid`: `pass` - requested lesser-body release matches the published lesser-host managed compatibility contract
- `lesser_body_template_preflight_valid`: `pass` - published template lesser-body-managed-dev.template.json passed lesser-host managed body template preflight
- `lesser_body_template_changeset_valid`: `pass` - published template lesser-body-managed-dev.template.json passed cloudformation_deploy_no_execute_changeset and is recorded at managed/updates/simulacrum/job-update-1/body-template-certification.json
- `hosted_update_completed`: `pass` - Lesser update job completed through lesser-host
- `receipt_key_defined`: `pass` - managed/updates/simulacrum/job-update-1/state.json
- `runner_visibility_present`: `pass` - https://console.aws.amazon.com/codebuild/home?#/builds/job-update-1-deploy/view/new
- `lesser_body_completed`: `pass` - lesser-body managed deploy completed
- `lesser_body_runner_visibility_present`: `pass` - https://console.aws.amazon.com/codebuild/home?#/builds/job-update-1-body/view/new
- `mcp_wiring_completed`: `pass` - MCP follow-on wiring completed

## Jobs

- `lesser` `job-update-1`: status=`ok` step=`done` version=`v1.2.6` receipt=`managed/updates/simulacrum/job-update-1/state.json`
  run_url: https://console.aws.amazon.com/codebuild/home?#/builds/job-update-1-deploy/view/new
  note: updated
- `lesser-body` `job-update-1`: status=`ok` step=`done` version=`v0.2.3` receipt=`managed/updates/simulacrum/job-update-1/body-state.json`
  run_url: https://console.aws.amazon.com/codebuild/home?#/builds/job-update-1-body/view/new
  body_run_url: https://console.aws.amazon.com/codebuild/home?#/builds/job-update-1-body/view/new
  template_path: lesser-body-managed-dev.template.json
  template_certification_key: managed/updates/simulacrum/job-update-1/body-template-certification.json
  template_verification_mode: cloudformation_deploy_no_execute_changeset
  note: lesser-body updated
- `mcp` `job-update-1`: status=`ok` step=`done` version=`v0.2.3` receipt=`managed/updates/simulacrum/job-update-1/mcp-state.json`
  run_url: https://console.aws.amazon.com/codebuild/home?#/builds/job-update-1-mcp/view/new
  mcp_run_url: https://console.aws.amazon.com/codebuild/home?#/builds/job-update-1-mcp/view/new
  note: MCP updated
