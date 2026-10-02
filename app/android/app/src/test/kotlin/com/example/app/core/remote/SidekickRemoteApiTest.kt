package com.example.app.core.remote

import kotlinx.coroutines.test.runTest
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import retrofit2.HttpException

class SidekickRemoteApiTest {
    private lateinit var server: MockWebServer
    private val json = Json

    private class EndpointCase(
        val name: String,
        val method: String,
        val path: String,
        val responseCode: Int = 200,
        val responseBody: String = "",
        val requestBody: String? = null,
        val call: suspend SidekickRemoteApi.() -> Any?,
        val verify: (Any?) -> Unit = {},
    )

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    @Test
    fun `workspace operation sends bearer token and decodes response`() = runTest {
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "application/json")
                .setBody(
                    """
                    {
                      "workspaces": [{
                        "id": "workspace-1",
                        "name": "Sidekick",
                        "localRepoDir": "/repo",
                        "configMode": "local",
                        "created": "2026-01-01T00:00:00Z",
                        "updated": "2026-01-01T00:00:00Z"
                      }]
                    }
                    """.trimIndent(),
                ),
        )
        val api = createApi()

        val workspaces = api.getWorkspaces().workspaces

        assertEquals(listOf("workspace-1"), workspaces.map(Workspace::id))
        assertEquals("Sidekick", workspaces.single().name)
        server.takeRequest().let { request ->
            assertEquals("/api/v1/workspaces", request.path)
            assertEquals("Bearer secret-token", request.getHeader("Authorization"))
        }
    }

    @Test
    fun `task operation decodes wrapped task list including nested flows`() = runTest {
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "application/json")
                .setBody(
                    """
                    {
                      "tasks": [{
                        "id": "task-1",
                        "workspaceId": "workspace 1",
                        "title": "Ship Android app",
                        "description": "Display tasks",
                        "status": "in_progress",
                        "agentType": "llm",
                        "flowType": "basic",
                        "created": "2026-01-01T00:00:00Z",
                        "updated": "2026-01-02T00:00:00Z",
                        "flows": [{"id": "flow-1", "status": "in_progress", "unknownField": 1}]
                      }]
                    }
                    """.trimIndent(),
                ),
        )
        val api = createApi()

        val tasks = api.getTasks("workspace 1").tasks

        assertEquals(1, tasks.size)
        assertEquals("task-1", tasks.single().id)
        assertEquals("Ship Android app", tasks.single().title)
        assertEquals("in_progress", tasks.single().status)
        assertEquals(listOf("flow-1"), tasks.single().flows.map(Flow::id))
        assertEquals(
            "/api/v1/workspaces/workspace%201/tasks/",
            server.takeRequest().path,
        )
    }

    @Test
    fun `workspace and provider endpoints`() = runCases(
        listOf(
            EndpointCase(
                name = "get workspace",
                method = "GET",
                path = "/api/v1/workspaces/ws-1",
                responseBody = """{"workspace":{"id":"ws-1","name":"Sidekick","localRepoDir":"/repo","configMode":"local","profileId":"work","llmConfig":{"defaults":[]},"created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z"}}""",
                call = { getWorkspace("ws-1") },
            ) { result ->
                val workspace = (result as WorkspaceResponse).workspace
                assertEquals("ws-1", workspace.id)
                assertEquals("work", workspace.profileId)
                assertEquals("/repo", workspace.localRepoDir)
            },
            EndpointCase(
                name = "providers for a profile",
                method = "GET",
                path = "/api/v1/providers?profileId=work",
                responseBody = """{"providers":["anthropic","openai"]}""",
                call = { getProviders("work") },
            ) { assertEquals(listOf("anthropic", "openai"), (it as ProvidersResponse).providers) },
            EndpointCase(
                name = "providers without a profile tolerate a nil slice",
                method = "GET",
                path = "/api/v1/providers",
                responseBody = """{"providers":null}""",
                call = { getProviders(null) },
            ) { assertEquals(emptyList<String>(), (it as ProvidersResponse).providers) },
            EndpointCase(
                name = "profiles",
                method = "GET",
                path = "/api/v1/profiles",
                responseBody = """{"profiles":[{"id":"default","name":"Default"},{"id":"work","name":"Work"}]}""",
                call = { getProfiles() },
            ) { assertEquals(listOf(Profile("default", "Default"), Profile("work", "Work")), (it as ProfilesResponse).profiles) },
            EndpointCase(
                name = "workspace providers",
                method = "GET",
                path = "/api/v1/workspaces/ws-1/providers",
                responseBody = """{"providers":["google"]}""",
                call = { getWorkspaceProviders("ws-1") },
            ) { assertEquals(listOf("google"), (it as ProvidersResponse).providers) },
            EndpointCase(
                name = "branches",
                method = "GET",
                path = "/api/v1/workspaces/ws-1/branches",
                responseBody = """{"branches":[{"name":"main","isCurrent":true,"isDefault":true},{"name":"feature","isCurrent":false,"isDefault":false}]}""",
                call = { getBranches("ws-1") },
            ) { result ->
                val branches = (result as BranchListResponse).branches
                assertEquals(listOf("main", "feature"), branches.map(BranchInfo::name))
                assertTrue(branches[0].isCurrent && branches[0].isDefault)
            },
            EndpointCase(
                name = "create branch",
                method = "POST",
                path = "/api/v1/workspaces/ws-1/branches",
                responseCode = 201,
                requestBody = """{"name":"feature","baseBranch":"main"}""",
                responseBody = """{"branch":{"name":"feature","isCurrent":false,"isDefault":false}}""",
                call = { createBranch("ws-1", CreateBranchRequest(name = "feature", baseBranch = "main")) },
            ) { assertEquals(BranchInfo(name = "feature"), (it as BranchResponse).branch) },
        ),
    )

    @Test
    fun `task endpoints`() = runCases(
        listOf(
            EndpointCase(
                name = "get task with flows",
                method = "GET",
                path = "/api/v1/workspaces/ws-1/tasks/task-1",
                responseBody = """{"task":{"id":"task-1","workspaceId":"ws-1","title":"Ship","status":"in_progress","projectId":"proj-1","flowOptions":{"determineRequirements":true},"created":"c","updated":"u","flows":[{"workspaceId":"ws-1","id":"flow-1","type":"basic_dev","parentId":"task-1","status":"in_progress","created":"c","updated":"u"}]}}""",
                call = { getTask("ws-1", "task-1") },
            ) { result ->
                val task = (result as TaskResponse).task
                assertEquals("proj-1", task.projectId)
                assertEquals(JsonPrimitive(true), task.flowOptions?.get("determineRequirements"))
                assertEquals("basic_dev", task.flows.single().type)
                assertEquals("task-1", task.flows.single().parentId)
            },
            EndpointCase(
                name = "nil flows decode as empty",
                method = "GET",
                path = "/api/v1/workspaces/ws-1/tasks/task-2",
                responseBody = """{"task":{"id":"task-2","workspaceId":"ws-1","title":"Draft","status":"drafting","flows":null}}""",
                call = { getTask("ws-1", "task-2") },
            ) { assertEquals(emptyList<Flow>(), (it as TaskResponse).task.flows) },
            EndpointCase(
                name = "create task",
                method = "POST",
                path = "/api/v1/workspaces/ws-1/tasks/",
                requestBody = """{"title":"Ship","description":"Do it","flowType":"basic_dev","flowOptions":{"determineRequirements":false}}""",
                responseBody = """{"task":{"id":"task-3","workspaceId":"ws-1","title":"Ship","status":"to_do"}}""",
                call = {
                    createTask(
                        "ws-1",
                        CreateTaskRequest(
                            title = "Ship",
                            description = "Do it",
                            flowType = "basic_dev",
                            flowOptions = buildJsonObject { put("determineRequirements", false) },
                        ),
                    )
                },
            ) { assertEquals("task-3", (it as TaskResponse).task.id) },
            EndpointCase(
                name = "update task sends only provided fields so the server keeps the rest",
                method = "PUT",
                path = "/api/v1/workspaces/ws-1/tasks/task-1",
                requestBody = """{"status":"to_do"}""",
                responseBody = """{"task":{"id":"task-1","workspaceId":"ws-1","title":"Ship","status":"to_do"}}""",
                call = { updateTask("ws-1", "task-1", UpdateTaskRequest(status = "to_do")) },
            ) { assertEquals("to_do", (it as TaskResponse).task.status) },
            EndpointCase(
                name = "update task can clear the project with an explicit empty id",
                method = "PUT",
                path = "/api/v1/workspaces/ws-1/tasks/task-1",
                requestBody = """{"projectId":""}""",
                responseBody = """{"task":{"id":"task-1","workspaceId":"ws-1","title":"Ship","status":"to_do"}}""",
                call = { updateTask("ws-1", "task-1", UpdateTaskRequest(projectId = "")) },
            ) { assertEquals("", (it as TaskResponse).task.projectId) },
            EndpointCase(
                name = "cancel task",
                method = "POST",
                path = "/api/v1/workspaces/ws-1/tasks/task-1/cancel",
                responseBody = """{"message":"Task canceled successfully"}""",
                call = { cancelTask("ws-1", "task-1") },
            ) { assertEquals("Task canceled successfully", (it as MessageResponse).message) },
            EndpointCase(
                name = "archive task returns no content",
                method = "POST",
                path = "/api/v1/workspaces/ws-1/tasks/task-1/archive",
                responseCode = 204,
                call = { archiveTask("ws-1", "task-1") },
            ) { assertEquals(Unit, it) },
            EndpointCase(
                name = "task flows",
                method = "GET",
                path = "/api/v1/workspaces/ws-1/tasks/task-1/flows",
                responseBody = """{"flows":[{"id":"flow-1","status":"complete"},{"id":"flow-2","status":"in_progress"}]}""",
                call = { getTaskFlows("ws-1", "task-1") },
            ) { assertEquals(listOf("flow-1", "flow-2"), (it as FlowListResponse).flows.map(Flow::id)) },
        ),
    )

    @Test
    fun `flow endpoints`() = runCases(
        listOf(
            EndpointCase(
                name = "get flow with worktrees and metadata",
                method = "GET",
                path = "/api/v1/workspaces/ws-1/flows/flow-1",
                responseBody = """{"flow":{"workspaceId":"ws-1","id":"flow-1","type":"basic_dev","parentId":"task-1","status":"in_progress","title":"Ship","metadata":{"branch":"main"},"created":"c","updated":"u","worktrees":[{"id":"wt-1","flowId":"flow-1","name":"side/ship","workspaceId":"ws-1","workingDirectory":"/repo/.side/worktrees/ship","created":"c"}]}}""",
                call = { getFlow("ws-1", "flow-1") },
            ) { result ->
                val flow = (result as FlowResponse).flow
                assertEquals("task-1", flow.parentId)
                assertEquals("Ship", flow.title)
                assertEquals(JsonPrimitive("main"), flow.metadata?.get("branch"))
                assertEquals("/repo/.side/worktrees/ship", flow.worktrees.single().workingDirectory)
            },
            EndpointCase(
                name = "flow without worktrees or metadata",
                method = "GET",
                path = "/api/v1/workspaces/ws-1/flows/flow-2",
                responseBody = """{"flow":{"workspaceId":"ws-1","id":"flow-2","type":"basic_dev","parentId":"task-1","status":"paused","worktrees":null}}""",
                call = { getFlow("ws-1", "flow-2") },
            ) { result ->
                val flow = (result as FlowResponse).flow
                assertTrue(flow.worktrees.isEmpty())
                assertNull(flow.metadata)
            },
            EndpointCase(
                name = "flow actions keep params as json and retain failures",
                method = "GET",
                path = "/api/v1/workspaces/ws-1/flows/flow-1/actions",
                responseBody = """{"flowActions":[{"id":"fa-1","flowId":"flow-1","workspaceId":"ws-1","subflowId":"sf-1","actionType":"user_request","actionStatus":"pending","actionParams":{"requestKind":"approval","requestContent":"Merge?"},"actionResult":"","isHumanAction":true,"isCallbackAction":true,"created":"c","updated":"u"},{"id":"fa-2","flowId":"flow-1","workspaceId":"ws-1","actionType":"generate.plan","actionStatus":"failed","actionParams":null,"actionResult":"missing credentials","isHumanAction":false,"isCallbackAction":false,"created":"c","updated":"u"}]}""",
                call = { getFlowActions("ws-1", "flow-1") },
            ) { result ->
                val actions = (result as FlowActionListResponse).flowActions
                assertEquals(listOf("fa-1", "fa-2"), actions.map(FlowAction::id))
                assertEquals("sf-1", actions[0].subflowId)
                assertEquals("approval", actions[0].actionParams?.get("requestKind")?.jsonPrimitive?.content)
                assertTrue(actions[0].isHumanAction && actions[0].isCallbackAction)
                assertNull(actions[1].actionParams)
                assertEquals("failed", actions[1].actionStatus)
                assertEquals("missing credentials", actions[1].actionResult)
            },
            EndpointCase(
                name = "flow subflows",
                method = "GET",
                path = "/api/v1/workspaces/ws-1/flows/flow-1/subflows",
                responseBody = """{"subflows":[{"workspaceId":"ws-1","id":"sf-1","name":"Plan","type":"step","status":"started","flowId":"flow-1","updated":"u"},{"workspaceId":"ws-1","id":"sf-2","name":"Edit","status":"complete","parentSubflowId":"sf-1","flowId":"flow-1","result":"done"}]}""",
                call = { getFlowSubflows("ws-1", "flow-1") },
            ) { result ->
                val subflows = (result as SubflowListResponse).subflows
                assertEquals("step", subflows[0].type)
                assertNull(subflows[1].type)
                assertEquals("sf-1", subflows[1].parentSubflowId)
                assertEquals("done", subflows[1].result)
            },
            EndpointCase(
                name = "nil subflows decode as empty",
                method = "GET",
                path = "/api/v1/workspaces/ws-1/flows/flow-1/subflows",
                responseBody = """{"subflows":null}""",
                call = { getFlowSubflows("ws-1", "flow-1") },
            ) { assertEquals(emptyList<Subflow>(), (it as SubflowListResponse).subflows) },
            EndpointCase(
                name = "get subflow",
                method = "GET",
                path = "/api/v1/workspaces/ws-1/subflows/sf-1",
                responseBody = """{"subflow":{"workspaceId":"ws-1","id":"sf-1","name":"Plan","status":"started","flowId":"flow-1"}}""",
                call = { getSubflow("ws-1", "sf-1") },
            ) { assertEquals("Plan", (it as SubflowResponse).subflow.name) },
            EndpointCase(
                name = "pause flow",
                method = "POST",
                path = "/api/v1/workspaces/ws-1/flows/flow-1/pause",
                responseBody = """{"message":"Flow paused successfully"}""",
                call = { pauseFlow("ws-1", "flow-1") },
            ) { assertEquals("Flow paused successfully", (it as MessageResponse).message) },
            EndpointCase(
                name = "cancel flow",
                method = "POST",
                path = "/api/v1/workspaces/ws-1/flows/flow-1/cancel",
                responseBody = """{"message":"Workflow cancelled successfully"}""",
                call = { cancelFlow("ws-1", "flow-1") },
            ) { assertEquals("Workflow cancelled successfully", (it as MessageResponse).message) },
            EndpointCase(
                name = "user action",
                method = "POST",
                path = "/api/v1/workspaces/ws-1/flows/flow-1/user_action",
                requestBody = """{"actionType":"go_next"}""",
                responseBody = """{"message":"User action 'go_next' signaled successfully"}""",
                call = { sendUserAction("ws-1", "flow-1", UserActionRequest("go_next")) },
            ) { assertEquals("User action 'go_next' signaled successfully", (it as MessageResponse).message) },
            EndpointCase(
                name = "complete flow action returns the raw action",
                method = "POST",
                path = "/api/v1/workspaces/ws-1/flow_actions/fa-1/complete",
                requestBody = """{"userResponse":{"approved":true,"params":{"targetBranch":"main"}}}""",
                responseBody = """{"id":"fa-1","flowId":"flow-1","workspaceId":"ws-1","actionType":"user_request","actionStatus":"complete","actionParams":{"requestKind":"merge_approval"},"actionResult":"{\"approved\":true}","isHumanAction":true,"isCallbackAction":true}""",
                call = {
                    completeFlowAction(
                        "ws-1",
                        "fa-1",
                        CompleteFlowActionRequest(
                            UserResponse(approved = true, params = buildJsonObject { put("targetBranch", "main") }),
                        ),
                    )
                },
            ) { result ->
                val action = result as FlowAction
                assertEquals("complete", action.actionStatus)
                assertEquals("""{"approved":true}""", action.actionResult)
                assertEquals("merge_approval", action.actionParams?.get("requestKind")?.jsonPrimitive?.content)
            },
            EndpointCase(
                name = "complete flow action keeps an explicit rejection",
                method = "POST",
                path = "/api/v1/workspaces/ws-1/flow_actions/fa-1/complete",
                requestBody = """{"userResponse":{"content":"no","approved":false}}""",
                responseBody = """{"id":"fa-1","flowId":"flow-1","actionType":"user_request","actionStatus":"complete"}""",
                call = {
                    completeFlowAction(
                        "ws-1",
                        "fa-1",
                        CompleteFlowActionRequest(UserResponse(content = "no", approved = false)),
                    )
                },
            ) { assertEquals("complete", (it as FlowAction).actionStatus) },
            EndpointCase(
                name = "complete flow action with a choice",
                method = "POST",
                path = "/api/v1/workspaces/ws-1/flow_actions/fa-1/complete",
                requestBody = """{"userResponse":{"choice":"Option A"}}""",
                responseBody = """{"id":"fa-1","flowId":"flow-1","actionType":"user_request","actionStatus":"complete"}""",
                call = {
                    completeFlowAction("ws-1", "fa-1", CompleteFlowActionRequest(UserResponse(choice = "Option A")))
                },
            ),
        ),
    )

    @Test
    fun `client and server errors surface as http exceptions with the error body`() = runTest {
        val cases = listOf<Pair<Int, suspend SidekickRemoteApi.() -> Any?>>(
            404 to { getFlow("ws-1", "missing") },
            400 to { completeFlowAction("ws-1", "fa-1", CompleteFlowActionRequest(UserResponse())) },
            400 to { updateTask("ws-1", "task-1", UpdateTaskRequest(status = "bogus")) },
            409 to { createBranch("ws-1", CreateBranchRequest(name = "main", baseBranch = "main")) },
            401 to { getTasks("ws-1") },
        )

        for ((code, call) in cases) {
            server.enqueue(
                MockResponse()
                    .setResponseCode(code)
                    .setHeader("Content-Type", "application/json")
                    .setBody("""{"error":"nope"}"""),
            )

            val error = runCatching { createApi().call() }.exceptionOrNull()

            assertTrue("expected HttpException for $code, got $error", error is HttpException)
            assertEquals(code, (error as HttpException).code())
            assertEquals("""{"error":"nope"}""", error.response()?.errorBody()?.string())
            server.takeRequest()
        }
    }

    private fun runCases(cases: List<EndpointCase>) = runTest {
        for (case in cases) {
            server.enqueue(
                MockResponse()
                    .setResponseCode(case.responseCode)
                    .setHeader("Content-Type", "application/json")
                    .setBody(case.responseBody),
            )

            val result = try {
                case.call(createApi())
            } catch (error: Throwable) {
                throw AssertionError("${case.name}: call failed", error)
            }

            val request = server.takeRequest()
            assertEquals(case.name, case.method, request.method)
            assertEquals(case.name, case.path, request.path)
            assertEquals(case.name, "Bearer secret-token", request.getHeader("Authorization"))
            val sentBody = request.body.readUtf8()
            if (case.requestBody == null) {
                assertEquals("${case.name}: expected no request body", "", sentBody)
            } else {
                assertTrue(
                    "${case.name}: expected json content type, got ${request.getHeader("Content-Type")}",
                    request.getHeader("Content-Type").orEmpty().startsWith("application/json"),
                )
                assertEquals(
                    case.name,
                    json.parseToJsonElement(case.requestBody),
                    json.parseToJsonElement(sentBody),
                )
            }
            case.verify(result)
        }
    }

    @Test
    fun `empty server collections remain empty`() = runTest {
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "application/json")
                .setBody("""{"tasks":[]}"""),
        )

        assertEquals(emptyList<Task>(), createApi().getTasks("workspace-1").tasks)
    }

    @Test
    fun `server errors are surfaced as http exceptions`() = runTest {
        server.enqueue(MockResponse().setResponseCode(500))

        val error = assertThrows(HttpException::class.java) {
            kotlinx.coroutines.runBlocking {
                createApi().getWorkspaces()
            }
        }

        assertEquals(500, error.code())
        assertEquals("/api/v1/workspaces", server.takeRequest().path)
    }

    @Test
    fun `malformed and incomplete responses are rejected`() = runTest {
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "application/json")
                .setBody("""{"workspaces":"""),
        )
        assertThrows(SerializationException::class.java) {
            kotlinx.coroutines.runBlocking {
                createApi().getWorkspaces()
            }
        }

        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "application/json")
                .setBody("""{"tasks":[{"id":"task-1"}]}"""),
        )
        assertThrows(SerializationException::class.java) {
            kotlinx.coroutines.runBlocking {
                createApi().getTasks("workspace-1")
            }
        }

        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "application/json")
                .setBody("""{"unknown":[]}"""),
        )
        assertThrows(SerializationException::class.java) {
            kotlinx.coroutines.runBlocking {
                createApi().getWorkspaces()
            }
        }
    }

    private fun createApi(): SidekickRemoteApi =
        SidekickRemoteApiFactory().create(
            token = "secret-token",
            callFactory = OkHttpClient(),
            baseUrl = server.url("/").toString(),
        )
}