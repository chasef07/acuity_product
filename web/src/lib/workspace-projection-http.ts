import { portalClient, realtimeURL } from "./api/client"
import {
  completeTask as completeTaskRequest,
  discoverAccess,
  getAiInteraction,
  getCallingCall,
  getWorkspace,
  queryTasks,
  readTask,
} from "./api/generated/sdk.gen"
import { getAccessTokenResult } from "./auth-client"
import { portalFailure } from "./clients/portal-request"
import {
  type WorkspaceAuthorityAdapter,
  type WorkspaceAuthorityResult,
  type WorkspaceRealtimeAdapter,
  WorkspaceProjectionAccessError,
} from "./workspace-projection.ts"
import {
  createWorkspaceSync,
  WorkspaceSyncUnauthorizedError,
} from "./workspace-sync/workspace-sync.ts"

export function createWorkspaceAuthorityAdapter(): WorkspaceAuthorityAdapter {
  return {
    authenticate: getAccessTokenResult,
    async discover(token, signal) {
      const result = await discoverAccess({
        client: portalClient(token),
        signal,
      }).catch(() => undefined)
      return authorityResult(result)
    },
    async workspace(token, scope, signal) {
      const result = await getWorkspace({
        client: portalClient(token),
        query: {
          practiceId: scope.practiceID,
          locationId: scope.locationID,
        },
        signal,
      }).catch(() => undefined)
      return authorityResult(result)
    },
    async tasks(token, request, signal) {
      const result = await queryTasks({
        client: portalClient(token),
        body: request,
        signal,
      }).catch(() => undefined)
      return authorityResult(result)
    },
    async aiInteraction(token, interactionID, signal) {
      const result = await getAiInteraction({
        client: portalClient(token),
        path: { interactionId: interactionID },
        signal,
      }).catch(() => undefined)
      return authorityResult(result)
    },
    async task(token, taskID, signal) {
      const result = await readTask({
        client: portalClient(token),
        path: { taskId: taskID },
        signal,
      }).catch(() => undefined)
      return authorityResult(result)
    },
    async call(token, callID, signal) {
      const result = await getCallingCall({
        client: portalClient(token),
        path: { callId: callID },
        signal,
      }).catch(() => undefined)
      return authorityResult(result)
    },
    async completeTask(token, task, signal) {
      const result = await completeTaskRequest({
        client: portalClient(token),
        path: { taskId: task.id },
        body: { expectedVersion: task.version },
        signal,
      }).catch(() => undefined)
      return authorityResult(result)
    },
  }
}

export function createWorkspaceRealtimeAdapter(): WorkspaceRealtimeAdapter {
  return {
    connect(callbacks) {
      let sync: ReturnType<typeof createWorkspaceSync> | undefined
      const activeSync = () => {
        sync ??= createWorkspaceSync({
          realtimeURL: realtimeURL(),
          getToken: callbacks.getToken,
          async reconcile(input) {
            try {
              return await callbacks.reconcile(input)
            } catch (error) {
              if (error instanceof WorkspaceProjectionAccessError) {
                throw new WorkspaceSyncUnauthorizedError()
              }
              throw error
            }
          },
          onStateChange: callbacks.onStateChange,
          onUnauthorized: callbacks.onUnauthorized,
          isHidden: () => document.hidden,
        })
        return sync
      }
      return {
        setScope(scope) {
          if (scope) activeSync().setScope(scope)
          else sync?.setScope()
        },
        refresh: () => sync?.refresh() ?? Promise.resolve(),
        visibilityChanged: () => sync?.visibilityChanged(),
        stop: () => sync?.stop(),
      }
    },
  }
}

function authorityResult<T>(
  result: { data?: T; error?: unknown; response?: Response } | undefined,
): WorkspaceAuthorityResult<T> {
  if (result?.data !== undefined) {
    return { kind: "success", data: result.data }
  }
  return portalFailure(result?.response?.status, result?.error)
}
