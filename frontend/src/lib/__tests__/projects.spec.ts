import { describe, it, expect } from 'vitest'
import { computeBucketRank, endOfBucketRank } from '../projects'
import type { Project, ProjectPriority } from '../models'

const project = (id: string, priority: ProjectPriority, rank: string): Project => ({
  workspaceId: 'ws1',
  id,
  title: id,
  priority,
  rank,
  created: '2026-01-01T00:00:00Z',
  updated: '2026-01-01T00:00:00Z',
})

describe('computeBucketRank', () => {
  const projects = [
    project('p1', 'high', 'n'),
    project('p2', 'high', 'u'),
    project('p3', 'high', 'x'),
    project('p4', 'low', 'n'),
  ]

  it('moves a project to the top of its bucket', () => {
    const rank = computeBucketRank(projects, 'p3', 0)
    expect(rank < 'n').toBe(true)
  })

  it('moves a project between two others in its bucket', () => {
    // bucket without p1 is [p2, p3]; index 1 lands between their ranks
    const rank = computeBucketRank(projects, 'p1', 1)
    expect(rank > 'u').toBe(true)
    expect(rank < 'x').toBe(true)
  })

  it('clamps out-of-range indices to the end of the bucket', () => {
    const rank = computeBucketRank(projects, 'p1', 99)
    expect(rank > 'x').toBe(true)
  })

  it('only considers projects in the moved project\'s own priority bucket', () => {
    // p4 is alone in the 'low' bucket, so the 'high' ranks are ignored and a
    // fresh midpoint rank is produced
    const rank = computeBucketRank(projects, 'p4', 99)
    expect(rank).toBe(computeBucketRank([project('p4', 'low', 'n')], 'p4', 99))
  })
})

describe('endOfBucketRank', () => {
  it('sorts after all projects in the bucket', () => {
    const projects = [project('p1', 'high', 'n'), project('p2', 'high', 'u')]
    const rank = endOfBucketRank(projects, 'high')
    expect(rank > 'u').toBe(true)
  })

  it('ignores projects in other buckets', () => {
    const projects = [project('p1', 'low', 'x')]
    const rank = endOfBucketRank(projects, 'high')
    expect(rank).toBe(endOfBucketRank([], 'high'))
  })

  it('returns a non-empty rank for an empty bucket', () => {
    expect(endOfBucketRank([], 'high').length).toBeGreaterThan(0)
  })
})