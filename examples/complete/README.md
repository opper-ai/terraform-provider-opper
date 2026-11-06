# Complete configuration example

This configuration creates a project, a project-owned runtime key, a project-scoped monthly budget rule, and a dynamic-route draft. It does not change an organization-wide policy. Route deployment is disabled by default; set `deploy_route = true` only after the conditional backend API is rolled out to publish the graph for live traffic. The route belongs to the organization; the key selects the project when your application calls Opper, so that project's policies apply to its calls.

The budget is **disabled by default**. Set `enable_budget = true` to enforce the USD 100 monthly limit for the new project. Opper still requires a plan that supports writing this rule when it is disabled; a Gateway-only organization can reject the configuration with `402 plan_required`.

The route uses a single model node. Choose an exact current model ID from the [Opper catalog](https://opper.ai/models) or the [model discovery API](https://api.opper.ai/v3/models) that your organization is allowed to use. Deploying the route with Terraform does not invoke the model.

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

The route is saved as a draft. Its `dynamic/terraform-example-router` invocation does not serve calls until a version is deployed, through the optional deployment resource or through the platform/Management API. The draft resource observes an existing active version without changing it. The optional `deploy_route = true` setting adds a separate resource that activates the exact draft, manages off/on transitions and undeploys only its owned version on destroy. See the [deployment resource and backend requirement](../../docs/resources/dynamic_route_deployment.md#backend-requirement).

Destroying this example revokes its runtime key, deletes its project-scoped rule, and deletes the whole route, including any version deployed outside Terraform. Deleting the project also revokes any additional keys created inside it. This example is intended for an isolated test organization.

Live acceptance on 2026-11-06 ran this configuration in a disposable organization with both the budget and route deployment enabled. One apply created all five resources, the second plan showed no changes, and destroy restored the original inventory. See the [acceptance record](../../docs/live-acceptance.md#complete-example-2026-11-06).
