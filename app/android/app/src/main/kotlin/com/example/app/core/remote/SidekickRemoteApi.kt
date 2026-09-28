package com.example.app.core.remote

import kotlinx.serialization.json.Json
import okhttp3.Call
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path
import retrofit2.http.Query
import okhttp3.MediaType.Companion.toMediaType

interface SidekickRemoteApi {
    @GET("api/v1/workspaces")
    suspend fun getWorkspaces(): WorkspaceListResponse

    @GET("api/v1/workspaces/{workspaceId}")
    suspend fun getWorkspace(
        @Path("workspaceId") workspaceId: String,
    ): WorkspaceResponse

    @GET("api/v1/workspaces/{workspaceId}/tasks/")
    suspend fun getTasks(
        @Path("workspaceId") workspaceId: String,
    ): TaskListResponse

    @GET("api/v1/workspaces/{workspaceId}/tasks/{taskId}")
    suspend fun getTask(
        @Path("workspaceId") workspaceId: String,
        @Path("taskId") taskId: String,
    ): TaskResponse

    @POST("api/v1/workspaces/{workspaceId}/tasks/")
    suspend fun createTask(
        @Path("workspaceId") workspaceId: String,
        @Body request: CreateTaskRequest,
    ): TaskResponse

    @PUT("api/v1/workspaces/{workspaceId}/tasks/{taskId}")
    suspend fun updateTask(
        @Path("workspaceId") workspaceId: String,
        @Path("taskId") taskId: String,
        @Body request: UpdateTaskRequest,
    ): TaskResponse

    @POST("api/v1/workspaces/{workspaceId}/tasks/{taskId}/cancel")
    suspend fun cancelTask(
        @Path("workspaceId") workspaceId: String,
        @Path("taskId") taskId: String,
    ): MessageResponse

    /** Responds 204 with no body. */
    @POST("api/v1/workspaces/{workspaceId}/tasks/{taskId}/archive")
    suspend fun archiveTask(
        @Path("workspaceId") workspaceId: String,
        @Path("taskId") taskId: String,
    )

    @GET("api/v1/workspaces/{workspaceId}/tasks/{taskId}/flows")
    suspend fun getTaskFlows(
        @Path("workspaceId") workspaceId: String,
        @Path("taskId") taskId: String,
    ): FlowListResponse

    @GET("api/v1/workspaces/{workspaceId}/flows/{flowId}")
    suspend fun getFlow(
        @Path("workspaceId") workspaceId: String,
        @Path("flowId") flowId: String,
    ): FlowResponse

    @GET("api/v1/workspaces/{workspaceId}/flows/{flowId}/actions")
    suspend fun getFlowActions(
        @Path("workspaceId") workspaceId: String,
        @Path("flowId") flowId: String,
    ): FlowActionListResponse

    @GET("api/v1/workspaces/{workspaceId}/flows/{flowId}/subflows")
    suspend fun getFlowSubflows(
        @Path("workspaceId") workspaceId: String,
        @Path("flowId") flowId: String,
    ): SubflowListResponse

    @POST("api/v1/workspaces/{workspaceId}/flows/{flowId}/pause")
    suspend fun pauseFlow(
        @Path("workspaceId") workspaceId: String,
        @Path("flowId") flowId: String,
    ): MessageResponse

    @POST("api/v1/workspaces/{workspaceId}/flows/{flowId}/cancel")
    suspend fun cancelFlow(
        @Path("workspaceId") workspaceId: String,
        @Path("flowId") flowId: String,
    ): MessageResponse

    @POST("api/v1/workspaces/{workspaceId}/flows/{flowId}/user_action")
    suspend fun sendUserAction(
        @Path("workspaceId") workspaceId: String,
        @Path("flowId") flowId: String,
        @Body request: UserActionRequest,
    ): MessageResponse

    @GET("api/v1/workspaces/{workspaceId}/subflows/{subflowId}")
    suspend fun getSubflow(
        @Path("workspaceId") workspaceId: String,
        @Path("subflowId") subflowId: String,
    ): SubflowResponse

    /** Unlike the other endpoints, the server returns the updated action unwrapped. */
    @POST("api/v1/workspaces/{workspaceId}/flow_actions/{flowActionId}/complete")
    suspend fun completeFlowAction(
        @Path("workspaceId") workspaceId: String,
        @Path("flowActionId") flowActionId: String,
        @Body request: CompleteFlowActionRequest,
    ): FlowAction

    @GET("api/v1/providers")
    suspend fun getProviders(
        @Query("profileId") profileId: String?,
    ): ProvidersResponse

    @GET("api/v1/profiles")
    suspend fun getProfiles(): ProfilesResponse

    @GET("api/v1/workspaces/{workspaceId}/providers")
    suspend fun getWorkspaceProviders(
        @Path("workspaceId") workspaceId: String,
    ): ProvidersResponse

    @GET("api/v1/workspaces/{workspaceId}/branches")
    suspend fun getBranches(
        @Path("workspaceId") workspaceId: String,
    ): BranchListResponse

    @POST("api/v1/workspaces/{workspaceId}/branches")
    suspend fun createBranch(
        @Path("workspaceId") workspaceId: String,
        @Body request: CreateBranchRequest,
    ): BranchResponse
}

class SidekickRemoteApiFactory(
    private val json: Json = Json {
        ignoreUnknownKeys = true
    },
) {
    /** Builds an API whose every call carries [token]; for callers whose [callFactory] is not already authenticated. */
    fun create(
        token: String,
        callFactory: Call.Factory,
        baseUrl: String = "http://sidekick/",
    ): SidekickRemoteApi {
        require(token.isNotBlank()) { "Pairing token must not be empty" }

        val authenticatedCalls = Call.Factory { request ->
            callFactory.newCall(
                request.newBuilder()
                    .header("Authorization", "Bearer $token")
                    .build(),
            )
        }
        return create(authenticatedCalls, baseUrl)
    }

    fun create(
        callFactory: Call.Factory,
        baseUrl: String,
    ): SidekickRemoteApi =
        Retrofit.Builder()
            .baseUrl(baseUrl)
            .callFactory(callFactory)
            .addConverterFactory(
                json.asConverterFactory("application/json".toMediaType()),
            )
            .build()
            .create(SidekickRemoteApi::class.java)
}