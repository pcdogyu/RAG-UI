import { describe, expect, it } from 'vitest'
import { groupDirectoryAgents } from './agentDirectory'

describe('groupDirectoryAgents', () => {
  it('keeps valid WeKnora agents in their built-in and created groups', () => {
    const groups = groupDirectoryAgents([
      { id: 'builtin-fast', name: '快速问答', description: '', avatar: '◈', is_builtin: true },
      { id: 'hygr-memory', name: 'HYGR 研究记忆', description: '', avatar: '◈', is_builtin: false },
      { id: '', name: '不完整记录', description: '', avatar: '', is_builtin: false },
    ])

    expect(groups.builtin.map(agent => agent.name)).toEqual(['快速问答'])
    expect(groups.created.map(agent => agent.name)).toEqual(['HYGR 研究记忆'])
  })
})
