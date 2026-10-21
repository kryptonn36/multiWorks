export const endpoints = [
  { id: 'health', group: 'System', title: 'Check health', method: 'GET', path: '/health', description: 'Check API availability and the database connection.', public: true },
  { id: 'register', group: 'Authentication', title: 'Register account', method: 'POST', path: '/api/auth/register', description: 'Create a test account. Registration returns a user; log in separately to get a token.', public: true, fields: ['email', 'password'] },
  { id: 'login', group: 'Authentication', title: 'Log in', method: 'POST', path: '/api/auth/login', description: 'Log in to save an access token for subsequent requests. Tokens expire after 15 minutes.', public: true, fields: ['email', 'password'] },
  { id: 'me', group: 'Authentication', title: 'Current user', method: 'GET', path: '/api/auth/me', description: 'Inspect the user associated with the current token.' },
  { id: 'workspaces', group: 'Workspaces', title: 'List workspaces', method: 'GET', path: '/api/workspaces', description: 'List workspaces that the current user belongs to.' },
  { id: 'create-workspace', group: 'Workspaces', title: 'Create workspace', method: 'POST', path: '/api/workspaces', fields: ['name'], description: 'Create a workspace. The current user becomes its owner.' },
  { id: 'workspace', group: 'Workspaces', title: 'Get workspace', method: 'GET', path: '/api/workspaces/:workspaceID', description: 'Retrieve a workspace accessible to the current user.' },
  { id: 'workspace-members', group: 'Workspaces', title: 'List members', method: 'GET', path: '/api/workspaces/:workspaceID/members', description: 'List the selected workspace’s members and their roles.' },
  { id: 'add-workspace-member', group: 'Workspaces', title: 'Add member', method: 'POST', path: '/api/workspaces/:workspaceID/members', fields: ['email', 'role'], roles: ['member', 'admin'], description: 'Add a workspace member by email. Permissions and validation are checked by the backend.' },
  { id: 'projects', group: 'Projects', title: 'List projects', method: 'GET', path: '/api/workspaces/:workspaceID/projects', description: 'List projects accessible to the current user in this workspace.' },
  { id: 'create-project', group: 'Projects', title: 'Create project', method: 'POST', path: '/api/workspaces/:workspaceID/projects', fields: ['name'], description: 'Requires workspace membership. The current user becomes the project owner.' },
  { id: 'project', group: 'Projects', title: 'Get project', method: 'GET', path: '/api/projects/:projectID', description: 'Retrieve a project accessible to the current user.' },
  { id: 'project-members', group: 'Projects', title: 'List members', method: 'GET', path: '/api/projects/:projectID/members', description: 'List the selected project’s members and roles.' },
  { id: 'add-project-member', group: 'Projects', title: 'Add member', method: 'POST', path: '/api/projects/:projectID/members', fields: ['email', 'role'], roles: ['viewer', 'editor'], description: 'Add a project member by email. The backend resolves the user ID and checks project ownership and workspace membership.' },
  { id: 'ws-ticket', method: 'POST', path: '/api/projects/:projectID/ws-ticket' },
  { id: 'project-ws', method: 'GET', path: '/api/projects/:projectID/ws', transport: 'websocket' },
  { id: 'history', method: 'GET', path: '/api/projects/:projectID/history' },
  { id: 'history-sequence', method: 'GET', path: '/api/projects/:projectID/history/:seq' },
  { id: 'restore-history', method: 'POST', path: '/api/projects/:projectID/history/:seq/restore' },
  { id: 'board', group: 'Storyboard', method: 'GET', path: '/api/projects/:projectID/board' },
  { id: 'submit-op', group: 'Storyboard', method: 'POST', path: '/api/projects/:projectID/ops' },
  { id: 'replay-ops', group: 'Storyboard', method: 'GET', path: '/api/projects/:projectID/ops' },
];

export function requestPath(endpoint, context) {
  return endpoint.path.replace(/:([A-Za-z]+)(?=\/|$)/g, (_, key) => {
    if (context[key] === undefined || context[key] === null) throw new Error(`Enter a ${key.replace('ID', '')} ID in Request context first.`);
    return encodeURIComponent(String(context[key]));
  });
}
