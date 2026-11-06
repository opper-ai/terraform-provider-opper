# Restricted project access for Terraform

The project access foundation is deployed. The [backend rollout plan](https://github.com/opper-ai/opper/blob/909be8026acd2b48946a469ce71c76b638e83e2a/docs/plans/2026-11-04-project-access.md) keeps Restricted writes disabled until all project readers enforce the policy. Its first editor is reserved for dashboard Owners and Admins. This proposal covers a later Management API and MCP extension, subject to a separate product decision to let scoped management keys edit project access.

## Policy and API

Use a single policy per project. `organization` permits members with the existing action permission and requires empty allowlists. `restricted` permits members with that permission and a matching role or direct grant. Built-in Owners and Admins retain access, including when both lists are empty. Role names and member UUIDs identify recipients externally. Validate the role and membership in the project's organization and store their existing internal IDs. Project grants affect eligibility only, so granting a Viewer access to a project does not grant inference or trace-content permissions.

| Operation | Proposed contract |
| --- | --- |
| `GET /management/v1/projects/{uuid}/access` | Return `organization_uuid`, `project_uuid`, `mode`, sorted `role_keys`, sorted `user_uuids`, and an opaque `change_token`. Missing, deleted and foreign projects return the same `404`. |
| `PUT /management/v1/projects/{uuid}/access` | Replace the complete policy using `expected_change_token`. Return the resulting snapshot. A stale token returns `409` and leaves the policy intact. An identical policy with a valid token is a no-op. |

Use dedicated `project_access:read` and `project_access:write` scopes. Require a genuine organization management key for these Management API operations, including calls through Management MCP. The dashboard keeps its live built-in Owner/Admin check and uses the same policy store. Register MCP tools from the Management API contract and exclude them from the delegated user catalog. Existing project write scopes carry no policy authority.

Build the token from organization and project identity, access revision, mode and canonical recipient IDs. Membership deletion cascades direct grants and can change the snapshot without incrementing the access revision. Use a versioned format that all serving instances can verify. Use the existing organization policy lock and re-read the target and recipients inside the transaction after acquiring it. Verify current credential authority, validate recipients, replace grants and record the audit event atomically. Prefer role grants for team access. A direct grant targets the account's active membership at apply time. Rejoining never recreates a removed grant.

Stale tokens, invalid recipients and audit failures must leave every grant unchanged. After an uncertain response, a read can show the current policy but cannot prove which write produced it. Surface the uncertain outcome and require a fresh, reviewed token for another write.

## Terraform lifecycle

Add a singleton `opper_project_access` resource keyed by the existing project's UUID and bound to the verified organization. Import by project UUID to adopt the policy. Require import before changing an already Restricted project. A first apply can take ownership of an Organization policy only after reading and pinning it during planning. A plan that cannot identify and review the project requires a separate plan after the project exists. Refresh compares the entire normalized policy. A failed identity or authority check preserves Terraform state and stops reconciliation. Apply uses the saved token and fails on a conflict rather than fetching a new token and overwriting the edit.

Destroy removes Terraform ownership and retains the remote policy. To reopen a project, apply `mode = "organization"` explicitly. A retained grant can block custom role deletion, so remove references in a reviewed policy update before retiring the role. Project deletion and grant cleanup also need an explicit contract. Today a project is soft-deleted and the role foreign key can retain the reference.

For projects that must be Restricted from their first moment, add initial policy to project creation and persist it in the same transaction. Require `project_access:write` in addition to `projects:write` for this path. A separate Terraform project followed by a policy update would leave an organization-wide visibility window. Implement and validate atomic creation before recommending that workflow.

## Delivery gates

1. Finish the existing enforcement work across serving surfaces and regions, including native Files and content URLs, controls targeting, scoped aggregates and caches, and MCP OAuth delegation. Keep enforcement active whenever a Restricted policy exists.
2. Deliver the dashboard policy transaction and activation gate under the backend rollout plan. Decide whether to extend policy editing to scoped management keys before implementing public mutations.
3. Add the Management API and MCP tools behind the same server write gate. Test authority separation, cross-organization rejection, a membership cascade invalidating a saved token, atomic rollback, and denial through mounted resource paths. Keep writes disabled until all serving deployments enforce restrictions.
4. Add Terraform import, policy updates, stale plan refusal, and retain-on-destroy. Verify with real Terraform and disposable PostgreSQL, then with a disposable organization and an allowed and denied member after rollout. Run the provider's complete example as a combined apply, unchanged plan and destroy before Registry publication.
