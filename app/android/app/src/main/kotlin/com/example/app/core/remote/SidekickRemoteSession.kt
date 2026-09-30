package com.example.app.core.remote

import com.example.app.core.data.FlowRepository
import com.example.app.core.data.TaskRepository
import com.example.app.core.remote.realtime.RealtimeConnectionManager
import com.example.app.core.remote.realtime.asWebSocketOpener
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import okhttp3.Interceptor
import okhttp3.OkHttpClient
import okhttp3.Response

private const val CONNECT_TIMEOUT_SECONDS = 30L
private const val READ_TIMEOUT_SECONDS = 60L
private const val WRITE_TIMEOUT_SECONDS = 30L
private const val WEBSOCKET_PING_INTERVAL_SECONDS = 30L

/**
 * Everything needed to talk to one paired sidekick server: a loopback proxy
 * tunnelling TCP over iroh, an authenticated [OkHttpClient] pointed at it (for
 * REST and WebSockets alike) and the Retrofit API on top. Close it to release
 * the proxy and the iroh connection.
 */
class SidekickRemoteSession internal constructor(
    token: String,
    private val proxy: IrohLoopbackProxy,
    private val apiFactory: SidekickRemoteApiFactory = SidekickRemoteApiFactory(),
    private val scope: CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.Default),
) : AutoCloseable {
    constructor(
        credentials: PairingCredentials,
        connector: IrohConnector = FfiIrohConnector(),
    ) : this(
        token = credentials.token,
        proxy = IrohLoopbackProxy(credentials.ticket, connector),
    )

    private val lock = Any()
    private var closed = false
    private val workspaces = LinkedHashMap<String, WorkspaceSession>()

    init {
        require(token.isNotBlank()) { "Pairing token must not be empty" }
        proxy.start()
    }

    val baseUrl: String
        get() = proxy.baseUrl

    val okHttpClient: OkHttpClient = OkHttpClient.Builder()
        .addInterceptor(BearerTokenInterceptor(token))
        .connectTimeout(CONNECT_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        .readTimeout(READ_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        .writeTimeout(WRITE_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        .pingInterval(WEBSOCKET_PING_INTERVAL_SECONDS, TimeUnit.SECONDS)
        .build()

    val api: SidekickRemoteApi by lazy { apiFactory.create(okHttpClient, baseUrl) }

    /** The synced repositories for one workspace, created on first use and kept until the session closes. */
    fun forWorkspace(workspaceId: String): WorkspaceSession = synchronized(lock) {
        check(!closed) { "remote session is closed" }
        workspaces.getOrPut(workspaceId) {
            val realtime = RealtimeConnectionManager(workspaceId, baseUrl, okHttpClient.asWebSocketOpener(), scope)
            WorkspaceSession(
                workspaceId = workspaceId,
                realtime = realtime,
                flows = FlowRepository(workspaceId, api, realtime, scope),
                tasks = TaskRepository(workspaceId, api, realtime, scope),
            )
        }
    }

    override fun close() {
        val released = synchronized(lock) {
            if (closed) return
            closed = true
            workspaces.values.toList().also { workspaces.clear() }
        }
        released.forEach(::closeQuietly)
        scope.cancel()
        proxy.close()
        okHttpClient.dispatcher.executorService.shutdown()
        okHttpClient.connectionPool.evictAll()
    }
}

/**
 * Realtime sockets and repositories scoped to one workspace of a session. The
 * repositories share the [realtime] manager so the workspace holds a single
 * task-changes socket plus one pair of sockets per observed flow.
 */
class WorkspaceSession internal constructor(
    val workspaceId: String,
    val realtime: RealtimeConnectionManager,
    val flows: FlowRepository,
    val tasks: TaskRepository,
) : AutoCloseable {
    override fun close() {
        closeQuietly(tasks)
        closeQuietly(flows)
        closeQuietly(realtime)
    }
}

private class BearerTokenInterceptor(private val token: String) : Interceptor {
    override fun intercept(chain: Interceptor.Chain): Response =
        chain.proceed(
            chain.request().newBuilder()
                .header("Authorization", "Bearer $token")
                .build(),
        )
}

/**
 * Lends the API of a session that stays open across calls made with the same
 * pairing, so retries reuse the iroh connection instead of redialing. Pairing
 * with a different server replaces (and closes) the previous session.
 */
class RemoteSessionProvider(
    private val sessionFactory: (PairingCredentials) -> SidekickRemoteSession = { SidekickRemoteSession(it) },
) : AutoCloseable {
    private val lock = Any()
    private var session: SidekickRemoteSession? = null
    private var sessionKey: Pair<String, String>? = null
    private var closed = false

    fun apiFor(credentials: PairingCredentials): SidekickRemoteApi = synchronized(lock) {
        check(!closed) { "session provider is closed" }
        val key = credentials.ticket to credentials.token
        val current = session
        if (current != null && sessionKey == key) return current.api

        closeQuietly(current)
        session = null
        sessionKey = null
        val fresh = sessionFactory(credentials)
        session = fresh
        sessionKey = key
        fresh.api
    }

    override fun close() {
        val stale = synchronized(lock) {
            closed = true
            session.also {
                session = null
                sessionKey = null
            }
        }
        closeQuietly(stale)
    }
}