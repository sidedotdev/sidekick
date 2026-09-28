package com.example.app.core.remote

import kotlinx.serialization.KSerializer
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.builtins.nullable
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import kotlinx.serialization.json.JsonObject

/*
 * Wire models mirroring the sidekick server's JSON (see the Go `domain` and `api`
 * packages). Timestamps stay as RFC 3339 strings. Free-form maps such as flow
 * action params are kept as [JsonObject] since their keys vary per action type.
 */

/**
 * Go marshals nil slices as `null`; decode those as empty lists while leaving
 * missing-key validation to the property's own required/default declaration.
 */
class NullAsEmptyListSerializer<T>(elementSerializer: KSerializer<T>) : KSerializer<List<T>> {
    private val delegate = ListSerializer(elementSerializer).nullable

    override val descriptor: SerialDescriptor = delegate.descriptor

    override fun deserialize(decoder: Decoder): List<T> = delegate.deserialize(decoder) ?: emptyList()

    override fun serialize(encoder: Encoder, value: List<T>) = delegate.serialize(encoder, value)
}

@Serializable
data class Workspace(
    val id: String,
    val name: String,
    val localRepoDir: String = "",
    val configMode: String = "",
    val profileId: String = "",
    val created: String = "",
    val updated: String = "",
)

@Serializable
data class Worktree(
    val id: String,
    val flowId: String = "",
    val name: String = "",
    val workspaceId: String = "",
    val workingDirectory: String = "",
    val created: String = "",
)

@Serializable
data class Flow(
    val id: String,
    val workspaceId: String = "",
    val type: String = "",
    val parentId: String = "",
    val status: String = "",
    val title: String = "",
    val metadata: JsonObject? = null,
    val created: String = "",
    val updated: String = "",
    @Serializable(with = NullAsEmptyListSerializer::class)
    val worktrees: List<Worktree> = emptyList(),
)

@Serializable
data class FlowAction(
    val id: String,
    val flowId: String,
    val workspaceId: String = "",
    val subflowId: String = "",
    val actionType: String,
    val actionStatus: String,
    val actionParams: JsonObject? = null,
    val actionResult: String = "",
    val isHumanAction: Boolean = false,
    val isCallbackAction: Boolean = false,
    val created: String = "",
    val updated: String = "",
)

@Serializable
data class Subflow(
    val id: String,
    val flowId: String = "",
    val workspaceId: String = "",
    val name: String = "",
    val type: String? = null,
    val description: String = "",
    val status: String = "",
    val parentSubflowId: String = "",
    val result: String = "",
    val updated: String = "",
)

@Serializable
data class Task(
    val id: String,
    val workspaceId: String,
    val title: String,
    val description: String = "",
    val projectId: String = "",
    val status: String,
    val agentType: String = "",
    val flowType: String = "",
    val flowOptions: JsonObject? = null,
    val archived: String? = null,
    val created: String = "",
    val updated: String = "",
    @Serializable(with = NullAsEmptyListSerializer::class)
    val flows: List<Flow> = emptyList(),
)

@Serializable
data class Profile(
    val id: String,
    val name: String,
)

@Serializable
data class BranchInfo(
    val name: String,
    val isCurrent: Boolean = false,
    val isDefault: Boolean = false,
)

@Serializable
data class WorkspaceListResponse(
    @Serializable(with = NullAsEmptyListSerializer::class)
    val workspaces: List<Workspace>,
)

@Serializable
data class WorkspaceResponse(
    val workspace: Workspace,
)

@Serializable
data class TaskListResponse(
    @Serializable(with = NullAsEmptyListSerializer::class)
    val tasks: List<Task>,
)

@Serializable
data class TaskResponse(
    val task: Task,
)

@Serializable
data class FlowListResponse(
    @Serializable(with = NullAsEmptyListSerializer::class)
    val flows: List<Flow>,
)

@Serializable
data class FlowResponse(
    val flow: Flow,
)

@Serializable
data class FlowActionListResponse(
    @Serializable(with = NullAsEmptyListSerializer::class)
    val flowActions: List<FlowAction>,
)

@Serializable
data class SubflowListResponse(
    @Serializable(with = NullAsEmptyListSerializer::class)
    val subflows: List<Subflow>,
)

@Serializable
data class SubflowResponse(
    val subflow: Subflow,
)

@Serializable
data class ProvidersResponse(
    @Serializable(with = NullAsEmptyListSerializer::class)
    val providers: List<String>,
)

@Serializable
data class ProfilesResponse(
    @Serializable(with = NullAsEmptyListSerializer::class)
    val profiles: List<Profile>,
)

@Serializable
data class BranchListResponse(
    @Serializable(with = NullAsEmptyListSerializer::class)
    val branches: List<BranchInfo>,
)

@Serializable
data class BranchResponse(
    val branch: BranchInfo,
)

@Serializable
data class MessageResponse(
    val message: String = "",
)

@Serializable
data class CreateTaskRequest(
    val title: String,
    val description: String = "",
    val flowType: String,
    val agentType: String = "",
    val status: String = "",
    val flowOptions: JsonObject? = null,
    val projectId: String? = null,
)

/**
 * Partial task update: the server keeps the persisted value for every field that
 * is absent from the body, so unset (null) fields are never serialized. An empty
 * [projectId] explicitly clears the project assignment.
 */
@Serializable
data class UpdateTaskRequest(
    val title: String? = null,
    val description: String? = null,
    val flowType: String? = null,
    val agentType: String? = null,
    val status: String? = null,
    val flowOptions: JsonObject? = null,
    val projectId: String? = null,
)

@Serializable
data class UserResponse(
    val content: String = "",
    val approved: Boolean? = null,
    val choice: String = "",
    val params: JsonObject? = null,
)

@Serializable
data class CompleteFlowActionRequest(
    val userResponse: UserResponse,
)

@Serializable
data class UserActionRequest(
    val actionType: String,
)

@Serializable
data class CreateBranchRequest(
    val name: String,
    val baseBranch: String,
)