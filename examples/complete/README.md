# Complete configuration example

This configuration creates a project, a project-owned runtime key, a project-scoped monthly budget rule, and a dynamic-route draft. It does not change an organization-wide policy or deploy a route. The route belongs to the organization; the key selects the project when your application calls Opper, so that project's policies apply to its calls.

The budget is **disabled by default**. Set `enable_budget = true` to enforce the USD 100 monthly limit for the new project. Opper still requires a plan that supports writing this rule when it is disabled; a Gateway-only organization can reject the configuration with `402 plan_required`.

The pool graph selects by cost among its configured candidates. This example accepts one explicit candidate; extend the `models` list for a larger pool. Choose an exact current model ID from the [Opper catalog](https://opper.ai/models), or inspect the [model discovery API](https://api.opper.ai/v3/models), and ensure your organization is allowed to use it. No model inference is performed by Terraform.

## Use

Follow the repository's [local development setup](../../README.md#develop-locally) for this unpublished provider. Configure the development override, then supply `OPPER_MANAGEMENT_API_KEY` privately. The management key needs `projects:read`, `projects:write`, `projects:delete`, `apikeys:read`, `apikeys:write`, `controls:read`, `controls:write`, `dynamic_routes:read` and `dynamic_routes:write` for the complete lifecycle.

From this directory, with `TF_VAR_model_id` set to your chosen model ID:

```sh
terraform validate
terraform plan
terraform apply
```

With the development override, run plan/apply directly; initialization cannot install a Registry release of this unpublished provider. Review the plan before applying. The example's resource names are organization-scoped where required and may conflict with existing resources; change them if necessary. Terraform does not adopt same-named resources automatically.

**The runtime secret is stored in Terraform state.** The output's `sensitive` flag only hides it in ordinary output. Use an encrypted state backend with access controls; never commit state or print `terraform output -raw runtime_api_key` in shared logs. The provider cannot recover that secret from Opper after creation or import. Key rotation requires coordinating the new secret with application rollout.

The route is saved as a draft. Its `dynamic/terraform-example-router` invocation does not serve calls until you deploy the route through the platform or Management API outside Terraform. Terraform observes an existing active version but never deploys, undeploys or rolls it back. See the [deployment API prerequisite](../../docs/resources/dynamic_route.md#deployment-prerequisite).

Destroying this example revokes its runtime key, deletes its project-scoped rule, and deletes the whole route, including any version deployed outside Terraform. Deleting the project also revokes any additional keys created inside it. This example is intended for an isolated test organization.

This configuration has been validated with the local provider binary. Separate live acceptance suites passed for all four resource families on 2026-11-03, with task-resource cleanup verified. The complete configuration itself has not been applied as one combined live deployment; its budget and model-pool graph require the relevant plan and permitted catalog model. See the [acceptance record](../../docs/live-acceptance.md).
