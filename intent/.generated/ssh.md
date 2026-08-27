---
intent_links:
  - intent: "#one-connection-per-remote-environment"
    code:
      - env/ssh_transport.go:SSHTransport
      - env/ssh_transport.go:sshTransportFor
      - env/ssh_transport.go:runRemoteCommand
      - env/ssh_transport_native.go:nativeSSHPoolKey
      - env/ssh_transport_native.go:nativeSSHConn
      - env/environment.go:SSHCapableEnv
  - intent: "#background-processes-keep-their-route-home"
    code:
      - env/ssh_transport.go:HoldReverseForwards
      - env/ssh_transport_legacy.go:reverseForwardHolder
      - env/ssh_transport_native.go:ensureForwards
      - env/environment.go:reverseForwardArgs
      - env/remote_sftp.go:filterSSHArgs
      - dev/dev_run_activities.go:buildDevRunCmd
      - coding/lsp/lsp_client.go:lspServerCommand
      - env/remote_repo.go:SyncRepoToRemoteActivity
      - env/remote_repo.go:DeepenRepoActivity
      - env/remote_walk.go:sshFetchCommitDefault
      - env/modal_watchdog.sh
  - intent: "#filesystem-operations-are-safely-retryable"
    code:
      - env/ssh_transport.go:SFTPOp
      - env/remote_sftp.go:withSFTPRetry
      - env/remote_sftp.go:sftpFailureWarrantsReconnect
      - env/ssh_transport_native.go:nativeSFTPOp
  - intent: "#provider-reachability-is-honoured-exactly"
    code:
      - env/ssh_conn_config.go:SSHConnConfig
      - env/ssh_conn_config.go:ValidateNative
      - env/ssh_conn_config.go:LegacyArgs
      - env/ssh_config_resolve.go:resolveSSHConnConfig
      - env/modal.go:modalSSHConnConfig
      - env/modal.go:modalHTTPConnectProxy
      - env/openshell.go:openShellSSHConnConfig
      - env/environment.go:devpodSSHConnConfig
      - env/ssh_transport_native.go:dialNativeUnderlyingConn
      - env/ssh_transport_native.go:nativeHostKeyCallback
  - intent: "#native-by-default-legacy-as-rollback"
    code:
      - env/ssh_transport.go:SSHTransportKind
      - env/ssh_transport.go:resolveSSHTransportKind
      - env/ssh_transport.go:nativeSSHTransportDefaults
      - env/ssh_transport.go:SSHTransportEnvVar
      - env/ssh_transport_legacy.go:legacySSHTransport
      - env/ssh_transport_native.go:nativeSSHTransport
  - intent: "#connections-outlive-only-what-they-should"
    code:
      - env/ssh_transport_native.go:reapIdleNativeSSHConns
      - env/ssh_transport_native.go:beginOp
      - env/ssh_transport_native.go:withClient
      - env/ssh_transport_native.go:keepNativeConnectionAlive
      - env/ssh_transport_native.go:nativeBoundedContext
      - env/ssh_transport_native.go:CloseAllNativeSSHClients
      - env/ssh_transport_legacy.go:CloseAllReverseForwardHolders
      - worker/worker.go:Stop
  - intent: "#a-caller-can-tell-whether-its-command-ran"
    code:
      - env/remote_exec.go:sshDialTransportError
      - env/ssh_transport_native.go:errHostKeyRejected
      - env/environment.go:isModalSSHTransportFailure
      - env/environment.go:runCommandInner
  - intent: "#the-remote-agent-installs-itself"
    code:
      - env/ssh_transport_native.go:withAgentBootstrap
      - env/ssh_transport_native.go:installRemoteAgentNatively
      - env/remote_sftp.go:installRemoteAgent
      - env/ssh_transport_native.go:errNativeAgentAbsent
---
# Inferred SSH Requirements

> Generated/inferred intent. Trusted less than human-authored intent; it records
> consequential, high-level inferences only and is not the source of truth.

## One Connection Per Remote Environment

Running a remote command, reading or writing remote files, and holding reverse
port forwards are three uses of the same connection to an environment, not three
independent connections. Environments that address the same remote on the same
terms reuse that connection; anything that changes how the connection is
established or trusted makes it a different connection.

## Background Processes Keep Their Route Home

A command may background a process that outlives it, and that process must still
reach the host. Reverse forwards therefore stay bound for as long as the
environment is in use, regardless of which command first needed them, and
regardless of whether the caller ran the command through the transport or built
its own ssh invocation.

Forwards are not a precondition for work: failing to bind one is reported and
the work proceeds, because the usual cause is that the remote port is already
forwarded, and refusing the caller's work helps no one.

Holding forwards must not look like usage. A Modal sandbox that is otherwise
idle must still hibernate on schedule while its forwards are held.

## Filesystem Operations Are Safely Retryable

A filesystem operation must survive losing its connection: it is retried on a
fresh one, and no caller ever holds a connection or client across that loss.

A retry must not be able to publish a stale result. An attempt abandoned on a
dead or timed-out connection may still be running remotely, and it must be
impossible for it to land on top of the result of the attempt that replaced it.

A missing remote path is an answer, not a symptom, so it ends the operation
rather than triggering a retry — unless creating missing paths is the point of
the operation.

## Provider Reachability Is Honoured Exactly

What a provider states about where a remote lives, which identity proves us,
which host keys are trusted, and which hop reaches it is authoritative, and it
must produce the same connection regardless of how that connection is made.

DevPod names an alias whose meaning belongs to the user's OpenSSH config, so the
effective configuration is whatever OpenSSH itself resolves for that alias.
Sidekick does not reimplement `ssh_config` semantics, because a partial
implementation would silently connect on terms the user never chose.

Anything the provider states that cannot be honoured fails the operation and
names what could not be honoured. Ignoring an unsupported directive is
unacceptable: it can weaken host-key verification or sever reachability while
appearing to succeed.

## Native By Default, Legacy As Rollback

Every provider uses the in-process SSH implementation by default. The
OpenSSH-subprocess implementation remains available and can be selected for a
whole process without a rebuild, so a regression in the default is mitigated in
place. Both satisfy the same contract; nothing above the transport may behave
differently depending on which is in use.

## Connections Outlive Only What They Should

Idle connections are released, but never underneath an operation in flight, and
never while they hold reverse forwards — either would break work already
happening or cut a background process off from the host.

A connection that dies transiently is re-established without failing work that
can still be retried safely. A peer or proxy that stops answering is detected
rather than waited on, so no operation hangs indefinitely. Shutdown releases
every connection, including the ones deliberately exempt from idle release.

## A Caller Can Tell Whether Its Command Ran

A remote command either ran or provably did not, and failures say which. That
distinction is what allows Modal to complete the work through its API instead:
re-running a command that may already have run is not safe, so the fallback is
available only when nothing ran.

A rejected host key never qualifies. It is a trust failure, and no alternative
path may be taken around it.

## The Remote Agent Installs Itself

Whether the agent binary is present on a remote is not the caller's concern.
Whichever operation first reaches a remote without it installs it, verifies what
it installed, and then completes the operation the caller asked for.