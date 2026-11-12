# Restricted project access for Terraform

The conditional policy Management API and Management MCP shipped in [Opper PR #6119](https://github.com/opper-ai/opper/pull/6119) on 2026-11-11. Genuine organization management keys may use dedicated `project_access:read` and `project_access:write` scopes. Policy writes remain behind the default-off organization activation gate. Dashboard policy editing, atomic Restricted creation and activation belong to the separate project-permissions rollout.

## Policy and API

Use a single policy per project. `organization` permits members with the existing action permission and requires empty allowlists. `restricted` permits members with that permission and a matching role or direct grant. Built-in Owners and Admins retain access, including when both lists are empty. Role names and member UUIDs identify recipients externally. Validate the role and membership in the project's organization and store their existing internal IDs. Project grants affect eligibility only, so granting a Viewer access to a project does not grant inference or trace-content permissions.

| Operation | Shipped contract |
| --- | --- |
| `GET /management/v1/projects/{uuid}/access` | Return `organization_uuid`, `project_uuid`, `mode`, sorted `role_keys`, sorted `user_uuids`, `revision`, and an opaque `change_token`. Missing, deleted and foreign projects return the same `404`. |
| `PUT /management/v1/projects/{uuid}/access` | Replace the complete policy using `expected_change_token`. Return the resulting snapshot. A stale token returns `409` and leaves the policy intact. An identical policy with a valid token is a no-op. |

The API uses dedicated `project_access:read` and `project_access:write` scopes and requires a genuine organization management key, including calls through Management MCP. Terraform uses read scope for planning, refresh and import, and write scope for policy changes. The dashboard keeps its live built-in Owner/Admin check and uses the same policy store. Policy tools are excluded from the delegated user catalog. Existing project write scopes carry no policy authority.

Build the token from organization and project identity, access revision, mode and canonical recipient IDs. Membership deletion cascades direct grants and can change the snapshot without incrementing the access revision. Use a versioned format that all serving instances can verify. Use the existing organization policy lock and re-read the target and recipients inside the transaction after acquiring it. Verify current credential authority, validate recipients, replace grants and record the audit event atomically. Prefer role grants for team access. A direct grant targets the account's active membership at apply time. Rejoining never recreates a removed grant.

Stale tokens, invalid recipients and audit failures must leave every grant unchanged. After an uncertain response, a read can show the current policy but cannot prove which write produced it. Surface the uncertain outcome and require a fresh, reviewed token for another write.

## Terraform lifecycle

The [`opper_project_access`](resources/project_access.md) resource manages the singleton policy of an existing project, bound to the verified organization. Import by project UUID to adopt an already Restricted policy. A first apply can take ownership of an Organization policy only after reading and pinning it during planning. Both UUIDs must be known during planning; a plan that cannot identify and review the project requires a separate plan after the project exists. The read-only `reviewed_policy_token` records the first adoption plan or import baseline and is preserved afterward. Refresh compares the entire normalized policy, and a new reviewed plan can reconcile drift. Failed reads, identity checks and authority checks preserve state, including `404`. Apply uses its pinned observation and fails on a conflict rather than fetching a new token and overwriting the edit.

Organization and project identity cannot change after adoption, and there is no automatic identity replacement. Manage another existing project at a new Terraform resource address, importing first if it is Restricted. Keep the original resource until the new policy apply succeeds, then remove the original block to relinquish ownership while retaining its policy.

Destroy removes Terraform ownership and retains the remote policy. To reopen a project, apply `mode = "organization"` explicitly. A retained grant can block custom role deletion, so remove references in a reviewed policy update before retiring the role. Project deletion and grant cleanup also need an explicit contract. Today a project is soft-deleted and the role foreign key can retain the reference.

For projects that must be Restricted from their first moment, add initial policy to project creation and persist it in the same transaction. Require `project_access:write` in addition to `projects:write` for this path. A separate Terraform project followed by a policy update would leave an organization-wide visibility window. Implement and validate atomic creation before recommending that workflow.

## Remaining delivery gates

Local real-Terraform lifecycle tests and the actual Management API/authentication/PostgreSQL workflow passed. See the [local verification record](live-acceptance.md#project-access-local-verification-2026-11-11).

Bounded live existing-project Terraform acceptance passed in EKS on 2026-11-12. The [live record](live-acceptance.md#project-access-lifecycle-2026-11-12) distinguishes the tested provider behavior, reused native/local coverage and cleanup from broader regional activation.

1. Keep broader project-permissions activation and creation support in their separately reviewed rollout. Enforcement stays active whenever a Restricted policy exists.
2. Keep Registry publication separate, and rerun the combined provider example before publication when its managed scope changes.
