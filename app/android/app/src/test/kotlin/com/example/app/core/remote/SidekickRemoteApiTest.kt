package com.example.app.core.remote

import kotlinx.coroutines.test.runTest
import kotlinx.serialization.SerializationException
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Before
import org.junit.Test
import retrofit2.HttpException

class SidekickRemoteApiTest {
    private lateinit var server: MockWebServer

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
            assertEquals("/api/v1/workspaces/", request.path)
            assertEquals("Bearer secret-token", request.getHeader("Authorization"))
        }
    }

    @Test
    fun `task operation decodes wrapped task list and ignores flow details`() = runTest {
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
                        "flows": [{"id": "flow-1"}]
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
        assertEquals(
            "/api/v1/workspaces/workspace%201/tasks/",
            server.takeRequest().path,
        )
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
        assertEquals("/api/v1/workspaces/", server.takeRequest().path)
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