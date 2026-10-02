package com.example.app.core.remote

/**
 * Base for test fakes: every operation fails loudly unless a test overrides it,
 * so fakes only implement the calls they intend to exercise.
 */
open class StubSidekickRemoteApi : SidekickRemoteApi {
    override suspend fun getWorkspaces(): WorkspaceListResponse = unsupported("getWorkspaces")

    override suspend fun getWorkspace(workspaceId: String): WorkspaceResponse = unsupported("getWorkspace")

    override suspend fun getTasks(workspaceId: String): TaskListResponse = unsupported("getTasks")

    override suspend fun getTask(workspaceId: String, taskId: String): TaskResponse = unsupported("getTask")

    override suspend fun createTask(workspaceId: String, request: CreateTaskRequest): TaskResponse =
        unsupported("createTask")

    override suspend fun updateTask(workspaceId: String, taskId: String, request: UpdateTaskRequest): TaskResponse =
        unsupported("updateTask")

    override suspend fun cancelTask(workspaceId: String, taskId: String): MessageResponse = unsupported("cancelTask")

    override suspend fun archiveTask(workspaceId: String, taskId: String): Unit = unsupported("archiveTask")

    override suspend fun getTaskFlows(workspaceId: String, taskId: String): FlowListResponse =
        unsupported("getTaskFlows")

    override suspend fun getFlow(workspaceId: String, flowId: String): FlowResponse = unsupported("getFlow")

    override suspend fun getFlowActions(workspaceId: String, flowId: String): FlowActionListResponse =
        unsupported("getFlowActions")

    override suspend fun getFlowSubflows(workspaceId: String, flowId: String): SubflowListResponse =
        unsupported("getFlowSubflows")

    override suspend fun pauseFlow(workspaceId: String, flowId: String): MessageResponse = unsupported("pauseFlow")

    override suspend fun cancelFlow(workspaceId: String, flowId: String): MessageResponse = unsupported("cancelFlow")

    override suspend fun sendUserAction(
        workspaceId: String,
        flowId: String,
        request: UserActionRequest,
    ): MessageResponse = unsupported("sendUserAction")

    override suspend fun getSubflow(workspaceId: String, subflowId: String): SubflowResponse =
        unsupported("getSubflow")

    override suspend fun completeFlowAction(
        workspaceId: String,
        flowActionId: String,
        request: CompleteFlowActionRequest,
    ): FlowAction = unsupported("completeFlowAction")

    override suspend fun getProviders(profileId: String?): ProvidersResponse = unsupported("getProviders")

    override suspend fun getProfiles(): ProfilesResponse = unsupported("getProfiles")

    override suspend fun getWorkspaceProviders(workspaceId: String): ProvidersResponse =
        unsupported("getWorkspaceProviders")

    override suspend fun getBranches(workspaceId: String): BranchListResponse = unsupported("getBranches")

    override suspend fun createBranch(workspaceId: String, request: CreateBranchRequest): BranchResponse =
        unsupported("createBranch")

    private fun unsupported(operation: String): Nothing =
        throw UnsupportedOperationException("$operation is not supported by this fake")
}