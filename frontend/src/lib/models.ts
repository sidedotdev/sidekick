export interface ModelConfig {
  provider: string
  model: string
}

export interface LLMConfig {
  defaults: ModelConfig[]
  useCaseConfigs: { [key: string]: ModelConfig[] }
}

export interface EmbeddingConfig {
  defaults: ModelConfig[]
  useCaseConfigs: { [key: string]: ModelConfig[] }
}

export interface Workspace {
  id?: string
  name: string
  localRepoDir: string
  llmConfig?: LLMConfig | null
  embeddingConfig?: EmbeddingConfig | null
}

// TODO /gen remove this and the components/views where it is used
export interface Message {
  id: string
  role: string
  content: string
  events?: string[]
}

// TODO /gen remove this and the components/views where it is used
export interface ActionData {
  status: string
  title: string
  actionType: string
}

// TODO add the rest
export type TaskStatus = 'drafting' | 'to_do' | 'blocked' | 'in_progress' | 'complete' | 'failed'
export type AgentType = 'human' | 'llm' | 'none'

export interface TaskLink {
  // TODO /gen define the fields of the TaskLink type based on backend
}

export interface Task {
  id: string
  created: Date
  updated: Date
  workspaceId: string
  title: string
  description: string
  status: TaskStatus
  links?: null | TaskLink[]
  agentType: AgentType
  flows: Flow[]
  flowOptions?: null | { [key: string]: any }
  archived?: Date | null
}

export type ActionStatus = 'pending' | 'started' | 'complete' | 'failed'

export interface Flow {
  workspaceId: string
  id: string
  type: string
  parentId: string
  status: string
  name?: string
  description?: string
  worktrees?: Worktree[]
}

export interface Worktree {
  id: string
  flowId: string
  name: string
  created: Date
  workspaceId: string
}

export interface FlowAction {
  id: string
  flowId: string
  workspaceId: string
  created: Date
  updated: Date
  subflow: string
  subflowDescription?: string
  actionType: string
  actionParams: { [key: string]: any }
  actionStatus: ActionStatus
  actionResult: string
  isHumanAction: boolean
}
export interface SubflowTree {
  name: string;
  description?: string | null;
  children: Array<FlowAction | SubflowTree>;
}

export type ChatRole = 'user' | 'assistant' | 'system' | 'tool'

/* TODO /gen rename to ChatMessageResponse and align with llm.ChatMessageResponse
 * from backend */
export interface ChatCompletionChoice extends ChatCompletionMessage {
  stopReason: string
  model?: string
  vendor?: string // TODO /gen remove in favor of provider
  provider?: string
}
export interface ChatCompletionMessage {
  content: string
  role: ChatRole
  function_call: FunctionCall
  toolCalls: ToolCall[]
  name?: string
  toolCallId?: string
  isError?: boolean
  usage?: Usage
}

export interface Usage {
  inputTokens?: number
  outputTokens?: number
}

export interface ToolCall {
  id: string
  name?: string
  arguments?: string
  parsedArguments?: any
}

export interface FunctionCall {
  name: string
  arguments: string
  parsedArguments: any
}

export interface ChatMessageDelta {
  role: ChatRole;
  content: string;
  toolCalls: ToolCall[];
  usage: Usage;
}