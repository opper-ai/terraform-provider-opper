# Restricted project access for Terraform

The conditional policy Management API and Management MCP shipped in [Opper PR #6119](https://github.com/opper-ai/opper/pull/6119) on 2026-11-11. Genuine organization management keys may use dedicated `project_access:read` and `project_access:write` scopes. Policy writes became generally available with [Opper PR #6174](https://github.com/opper-ai/opper/pull/6174), deployed to EKS and Evroc on 2026-11-13. No per-organization activation is required, and the rollout does not convert existing policies automatically. Native policy editing retains its built-in Owner/Admin requirement.

## Policy and API

Use a single policy per project. `organization` permits members with the existing action permission and requires empty allowlists. `restricted` permits members with that permission and a matching role or direct grant. Built-in Owners and Admins retain access, including when both lists are empty. Role names and member UUIDs identify recipients externally. Validate the role and membership in the project's organization and store their existing internal IDs. Project grants affect eligibility only, so granting a Viewer access to a project does not grant inference or trace-content permissions.

| Operation | Contract |
| --- | --- |
| `POST /management/v1/projects/with-access` | Dedicated endpoint deployed in both regions after [backend PR #6181](https://github.com/opper-ai/opper/pull/6181). Create project metadata and the required initial policy atomically. Requires `projects:write` and `project_access:write` on a genuine management key. No idempotency key; uncertain results require inspection/import. |
| `GET /management/v1/projects/{uuid}/access` | Return `organization_uuid`, `project_uuid`, `mode`, sorted `role_keys`, sorted `user_uuids`, `revision`, and an opaque `change_token`. Missing, deleted and foreign projects return the same `404`. |
| `PUT /management/v1/projects/{uuid}/access` | Replace the complete policy using `expected_change_token`. Return the resulting snapshot. A stale token returns `409` and leaves the policy intact. An identical policy with a valid token is a no-op. |

The API uses dedicated `project_access:read` and `project_access:write` scopes and requires a genuine organization management key, including calls through Management MCP. Terraform uses read scope for planning, refresh and import, and write scope for policy changes. The dashboard keeps its live built-in Owner/Admin check and uses the same policy store. Policy tools are excluded from the delegated user catalog. Existing project write scopes carry no policy authority.

Build the token from organization and project identity, access revision, mode and canonical recipient IDs. Membership deletion cascades direct grants and can change the snapshot without incrementing the access revision. Use a versioned format that all serving instances can verify. Use the existing organization policy lock and re-read the target and recipients inside the transaction after acquiring it. Verify current credential authority, validate recipients, replace grants and record the audit event atomically. Prefer role grants for team access. A direct grant targets the account's active membership at apply time. Rejoining never recreates a removed grant.

Stale tokens, invalid recipients and audit failures must leave every grant unchanged. After an uncertain response, a read can show the current policy but cannot prove which write produced it. Surface the uncertain outcome and require a fresh, reviewed token for another write.

## Terraform lifecycle

The [`opper_project_access`](resources/project_access.md) resource manages the singleton policy of an existing project, bound to the verified organization. Import by project UUID to adopt an already Restricted policy. A first apply can take ownership of an Organization policy only after reading and pinning it during planning. Both UUIDs must be known during planning; a plan that cannot identify and review the project requires a separate plan after the project exists. The read-only `reviewed_policy_token` records the first adoption plan or import baseline and is preserved afterward. Refresh compares the entire normalized policy, and a new reviewed plan can reconcile drift. Failed reads, identity checks and authority checks preserve state, including `404`. Apply uses its pinned observation and fails on a conflict rather than fetching a new token and overwriting the edit.

Organization and project identity cannot change after adoption, and there is no automatic identity replacement. Manage another existing project at a new Terraform resource address, importing first if it is Restricted. Keep the original resource until the new policy apply succeeds, then remove the original block to relinquish ownership while retaining its policy.

Destroy removes Terraform ownership and retains the remote policy. To reopen a project, apply `mode = "organization"` explicitly. Live project grants block custom role deletion, so remove them in a reviewed policy update before retiring the role. The backend increment supporting atomic creation also retires only the deleted role's grants on soft-deleted projects within the guarded role-deletion transaction. Other grants and the project's Restricted mode remain intact. Keep a project's dependency on its Terraform-managed custom role when omitting creation history so destruction orders the project before the role.

`opper_project.initial_access` creates the project with its complete initial policy through one `POST /management/v1/projects/with-access`. This dedicated endpoint is deployed in both regions; unsupported servers refuse it rather than silently ignoring an unknown field on the legacy project-creation route. There is no fallback, policy GET/PUT or creation retry.

The optional `initial_access` object has required `mode` and optional `role_keys` / `user_uuids`, each defaulting to an empty set. Role and user values may become known during apply. The provider retains the requested snapshot as creation history: metadata refresh and updates ignore subsequent policy drift. Changing or adding a snapshot on an existing/imported project is refused without replacement. Removing it only forgets the snapshot and preserves the policy.

To manage the current policy afterward, explicitly import the project into `opper_project_access` and review a separate plan after its UUID exists. Creating a project without `initial_access` and restricting it later leaves an organization-wide visibility window.

## Verification and remaining scope

Local real-Terraform lifecycle tests and the actual Management API/authentication/PostgreSQL workflow passed. See the [local verification record](live-acceptance.md#project-access-local-verification-2026-11-11).

Bounded live existing-project Terraform acceptance passed in EKS on 2026-11-12. The [live record](live-acceptance.md#project-access-lifecycle-2026-11-12) distinguishes the tested provider behavior, reused native/local coverage and cleanup from the later general-availability rollout.

1. Atomic initial policy creation passed [live Terraform acceptance](live-acceptance.md#atomic-initial-project-access-2026-11-13) on `api.opper.ai` after the dedicated endpoint deployed in both regions. The run used a local provider build; this feature is not included in v0.1.0. Live HTTP/audit counts and Evroc Terraform acceptance are not claimed. Enforcement stays active whenever a Restricted policy exists.
2. Keep Registry publication separate, and rerun the combined provider example before publication when its managed scope changes.
