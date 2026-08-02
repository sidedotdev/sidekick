import type { Project, ProjectPriority } from './models'
import { midString } from './rank'

export const projectPriorityLabels: Record<ProjectPriority, string> = {
  urgent: 'Urgent',
  high: 'High',
  medium: 'Medium',
  low: 'Low',
  none: 'No Priority',
}

// computeBucketRank returns the rank to persist when the given project is
// dropped at targetIndex within its own priority bucket; rank ordering never
// crosses priority buckets. The index is relative to the bucket with the
// moved project excluded, and the projects list must be in display (sorted)
// order.
export function computeBucketRank(
  projects: Project[],
  projectId: string,
  targetIndex: number,
): string {
  const project = projects.find(p => p.id === projectId)
  const bucket = project
    ? projects.filter(p => p.priority === project.priority && p.id !== projectId)
    : []
  const index = Math.max(0, Math.min(targetIndex, bucket.length))
  const prevRank = index > 0 ? bucket[index - 1].rank ?? '' : ''
  const nextRank = index < bucket.length ? bucket[index].rank ?? '' : ''
  return midString(prevRank, nextRank)
}

// endOfBucketRank returns a rank that sorts after every project currently in
// the given priority bucket. The projects list must be in display order.
export function endOfBucketRank(projects: Project[], priority: ProjectPriority): string {
  const bucket = projects.filter(p => p.priority === priority)
  const lastRank = bucket.length > 0 ? bucket[bucket.length - 1].rank ?? '' : ''
  return midString(lastRank, '')
}