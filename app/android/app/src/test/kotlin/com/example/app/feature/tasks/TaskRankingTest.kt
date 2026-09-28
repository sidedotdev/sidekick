package com.example.app.feature.tasks

import com.example.app.core.remote.Task
import org.junit.Assert.assertEquals
import org.junit.Test

class TaskRankingTest {

    private fun task(
        id: String,
        status: String,
        updated: String = "2026-03-10T12:00:00Z",
        title: String = "Task $id",
        description: String = "",
    ) = Task(
        id = id,
        workspaceId = "ws",
        title = title,
        description = description,
        status = status,
        updated = updated,
    )

    private fun ids(tasks: List<Task>) = tasks.map { it.id }

    @Test
    fun `statuses map to fixed buckets`() {
        val cases = mapOf(
            "blocked" to TaskBucket.NEEDS_ATTENTION,
            "in_review" to TaskBucket.NEEDS_ATTENTION,
            "in_progress" to TaskBucket.ACTIVE,
            "to_do" to TaskBucket.ACTIVE,
            "drafting" to TaskBucket.DRAFTS,
            "complete" to TaskBucket.DONE,
            "failed" to TaskBucket.DONE,
            "canceled" to TaskBucket.DONE,
            "paused" to TaskBucket.ACTIVE,
            "" to TaskBucket.ACTIVE,
        )
        cases.forEach { (status, bucket) ->
            assertEquals(status, bucket, bucketFor(status))
        }
    }

    @Test
    fun `tasks are ranked by bucket then updated descending`() {
        val tasks = listOf(
            task("done-new", "complete", "2026-03-10T12:00:00Z"),
            task("draft", "drafting", "2026-03-10T11:00:00Z"),
            task("todo-old", "to_do", "2026-03-01T00:00:00Z"),
            task("review", "in_review", "2026-02-01T00:00:00Z"),
            task("progress-new", "in_progress", "2026-03-09T00:00:00Z"),
            task("blocked", "blocked", "2026-03-05T00:00:00Z"),
            task("failed-old", "failed", "2026-01-01T00:00:00Z"),
        )

        assertEquals(
            listOf("blocked", "review", "progress-new", "todo-old", "draft", "done-new", "failed-old"),
            ids(rankTasks(tasks)),
        )
    }

    @Test
    fun `unknown statuses rank after known active tasks but before drafts`() {
        val tasks = listOf(
            task("mystery", "paused", "2026-03-10T12:00:00Z"),
            task("draft", "drafting", "2026-03-10T12:00:00Z"),
            task("todo", "to_do", "2026-01-01T00:00:00Z"),
            task("review", "in_review", "2026-01-01T00:00:00Z"),
        )

        assertEquals(listOf("review", "todo", "mystery", "draft"), ids(rankTasks(tasks)))
        assertEquals(listOf("todo", "mystery"), ids(bucketTasks(tasks).active))
    }

    @Test
    fun `timestamp formats compare correctly and unparsable dates sort last`() {
        val tasks = listOf(
            task("garbage", "to_do", "yesterday"),
            task("blank", "to_do", ""),
            task("offset", "to_do", "2026-03-10T13:00:00+02:00"),
            task("utc", "to_do", "2026-03-10T11:30:00Z"),
            task("local", "to_do", "2026-03-10T11:45:00"),
        )

        assertEquals(listOf("local", "utc", "offset", "garbage", "blank"), ids(rankTasks(tasks)))
    }

    @Test
    fun `equal timestamps keep input order`() {
        val tasks = listOf(
            task("first", "to_do"),
            task("second", "to_do"),
            task("third", "to_do"),
        )

        assertEquals(listOf("first", "second", "third"), ids(rankTasks(tasks)))
    }

    @Test
    fun `bucketTasks splits into sorted buckets`() {
        val tasks = listOf(
            task("draft-old", "drafting", "2026-03-01T00:00:00Z"),
            task("canceled", "canceled", "2026-03-02T00:00:00Z"),
            task("todo", "to_do", "2026-03-03T00:00:00Z"),
            task("draft-new", "drafting", "2026-03-04T00:00:00Z"),
            task("blocked", "blocked", "2026-03-05T00:00:00Z"),
            task("complete", "complete", "2026-03-06T00:00:00Z"),
            task("review", "in_review", "2026-03-07T00:00:00Z"),
            task("progress", "in_progress", "2026-03-08T00:00:00Z"),
        )

        val bucketed = bucketTasks(tasks)

        assertEquals(listOf("review", "blocked"), ids(bucketed.needsAttention))
        assertEquals(listOf("progress", "todo"), ids(bucketed.active))
        assertEquals(listOf("draft-new", "draft-old"), ids(bucketed.drafts))
        assertEquals(listOf("complete", "canceled"), ids(bucketed.done))
    }

    @Test
    fun `bucketTasks of nothing yields empty buckets`() {
        assertEquals(BucketedTasks(), bucketTasks(emptyList()))
    }

    @Test
    fun `search matches title or description case-insensitively`() {
        val tasks = listOf(
            task("title-hit", "to_do", title = "Fix Login Crash"),
            task("description-hit", "to_do", title = "Unrelated", description = "Users hit a LOGIN loop"),
            task("miss", "to_do", title = "Update docs", description = "Nothing here"),
        )

        val cases = mapOf(
            "login" to listOf("title-hit", "description-hit"),
            "LOGIN" to listOf("title-hit", "description-hit"),
            "  crash  " to listOf("title-hit"),
            "loop" to listOf("description-hit"),
            "docs" to listOf("miss"),
            "zzz" to emptyList(),
        )
        cases.forEach { (query, expected) ->
            assertEquals(query, expected, ids(searchTasks(tasks, query)))
        }
    }

    @Test
    fun `blank search returns every task ranked`() {
        val tasks = listOf(
            task("done", "complete"),
            task("todo", "to_do"),
            task("blocked", "blocked"),
        )

        listOf("", "   ").forEach { query ->
            assertEquals(query, listOf("blocked", "todo", "done"), ids(searchTasks(tasks, query)))
        }
    }

    @Test
    fun `search includes drafts and done but ranks them after open work`() {
        val tasks = listOf(
            task("done", "complete", "2026-03-10T12:00:00Z", title = "Deploy service"),
            task("draft", "drafting", "2026-03-10T11:00:00Z", title = "Deploy docs"),
            task("todo", "to_do", "2026-01-01T00:00:00Z", title = "Deploy staging"),
            task("review", "in_review", "2026-01-01T00:00:00Z", title = "Deploy prod"),
            task("other", "blocked", "2026-03-10T12:00:00Z", title = "Unrelated"),
        )

        assertEquals(listOf("review", "todo", "draft", "done"), ids(searchTasks(tasks, "deploy")))
    }
}