export type DirectoryAgent = {
  id: string
  name: string
  description: string
  avatar: string
  is_builtin: boolean
}

export type AgentDirectoryGroups = {
  builtin: DirectoryAgent[]
  created: DirectoryAgent[]
}

// Keep the upstream order while ensuring incomplete upstream records never
// become unusable cards in the directory.
export function groupDirectoryAgents(agents: DirectoryAgent[]): AgentDirectoryGroups {
  return agents.reduce<AgentDirectoryGroups>((groups, agent) => {
    if (!agent.id.trim() || !agent.name.trim()) return groups
    groups[agent.is_builtin ? 'builtin' : 'created'].push(agent)
    return groups
  }, { builtin: [], created: [] })
}
