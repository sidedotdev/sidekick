package com.example.app.feature.tasks

import com.example.app.core.remote.Task
import com.example.app.core.ui.parseIsoInstant
import java.time.Instant

enum class TaskBucket { NEEDS_ATTENTION, ACTIVE, DRAFTS, DONE }

private val knownStatuses = setOf(
    "blocked", "in_review",
    "in_progress", "to_do",
    "drafting",
    "complete", "failed", "canceled",
)

fun bucketFor(status: String): TaskBucket = when (status) {
    "blocked", "in_review" -> TaskBucket.NEEDS_ATTENTION
    "in_progress", "to_do" -> TaskBucket.ACTIVE
    "drafting" -> TaskBucket.DRAFTS
    "complete", "failed", "canceled" -> TaskBucket.DONE
    else -> TaskBucket.ACTIVE
}

data class BucketedTasks(
    val needsAttention: List<Task> = emptyList(),
    val active: List<Task> = emptyList(),
    val drafts: List<Task> = emptyList(),
    val done: List<Task> = emptyList(),
)

private data class RankKey(
    val bucket: TaskBucket,
    val unknownStatus: Boolean,
    val updated: Instant?,
)

private val rankKeyComparator: Comparator<RankKey> = compareBy<RankKey> { it.bucket }
    .thenBy { it.unknownStatus }
    .thenBy(nullsLast(reverseOrder())) { it.updated }

private fun rankKeyOf(task: Task) = RankKey(
    bucket = bucketFor(task.status),
    unknownStatus = task.status !in knownStatuses,
    updated = parseIsoInstant(task.updated),
)

/**
 * Orders tasks by bucket, then known statuses before unknown ones, then most recently
 * updated first with unparsable timestamps last. Ties keep their input order.
 */
fun rankTasks(tasks: List<Task>): List<Task> = tasks
    .map { it to rankKeyOf(it) }
    .sortedWith(compareBy(rankKeyComparator) { it.second })
    .map { it.first }

fun bucketTasks(tasks: List<Task>): BucketedTasks {
    val byBucket = rankTasks(tasks).groupBy { bucketFor(it.status) }
    return BucketedTasks(
        needsAttention = byBucket[TaskBucket.NEEDS_ATTENTION].orEmpty(),
        active = byBucket[TaskBucket.ACTIVE].orEmpty(),
        drafts = byBucket[TaskBucket.DRAFTS].orEmpty(),
        done = byBucket[TaskBucket.DONE].orEmpty(),
    )
}

/** Case-insensitive substring match on title or description, ranked like [rankTasks]. */
fun searchTasks(tasks: List<Task>, query: String): List<Task> {
    val needle = query.trim()
    if (needle.isEmpty()) return rankTasks(tasks)
    return rankTasks(
        tasks.filter {
            it.title.contains(needle, ignoreCase = true) ||
                it.description.contains(needle, ignoreCase = true)
        },
    )
}