import { createClient } from './api.js';
import { createStoryboard } from './storyboard.js';

const $ = (selector) => document.querySelector(selector);
const app = $('#app');
const modal = $('#modal');
const state = { user: null, workspaces: [], workspace: null, project: null, members: [], workspaceMembers: [], projects: [], mode: 'login', loading: false, error: '', search: '' };
let version = 0;
let toastTimer;
let storage;
try { storage = sessionStorage; } catch { /* Session remains in memory. */ }
const api = createClient({ storage, onUnauthorized: () => {
  version++; state.user = null; state.mode = 'login'; state.workspaces = []; modal.close();
  state.error = 'Your session has expired. Please sign in again.';
  location.hash = '#/login'; renderAuth();
} });
const esc = (value) => String(value ?? '').replace(/[&<>"']/g, (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char]));
const storyboard = createStoryboard({ api, escape: esc, showForm, closeModal: () => modal.close(), toast, storage, getUserID: () => state.user?.id || '' });
const date = (value) => value && !Number.isNaN(Date.parse(value)) ? new Date(value).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : '—';
const initials = (name) => (name || '?').slice(0, 2).toUpperCase();
const icons = { home: '⌂', workspaces: '▦', account: '◎', project: '◈', people: '♧' };
const brand = '<a class="brand" href="#/home"><span class="brand-mark">m.</span>multiworks<span class="brand-period">®</span></a>';
const route = () => location.hash.replace(/^#\/?/, '').split('/').filter(Boolean);
const badge = (role) => `<span class="badge ${esc(role)}">${esc(role || 'member')}</span>`;
function toast(message) {
  clearTimeout(toastTimer); $('#toast').textContent = message; $('#toast').hidden = false;
  toastTimer = setTimeout(() => { $('#toast').hidden = true; }, 4500);
}
function navigate(path) {
  if (location.hash === `#/${path}`) loadPage(); else location.hash = `#/${path}`;
}
function errorBox(message) { return message ? `<div class="error-message" role="alert">${esc(message)}</div>` : ''; }
function empty(title, message, action = '') {
  return `<div class="empty"><span class="empty-symbol">▦</span><h3>${title}</h3><p>${message}</p>${action}</div>`;
}
function nameField(placeholder) {
  return `<label>Name<input name="name" placeholder="${placeholder}" autocomplete="off" autofocus></label>`;
}
function renderAuth() {
  const register = state.mode === 'register';
  document.title = `${register ? 'Create an account' : 'Sign in'} · MultiWorks`;
  app.innerHTML = `<div class="auth-layout"><section class="auth-form-side">${brand}<div class="auth-form-wrap"><h1>${register ? 'Create your account' : 'Welcome back'}</h1><p>${register ? 'Create an account to start your first workspace.' : 'Sign in to see your workspaces and projects.'}</p><form id="auth-form" novalidate>${errorBox(state.error)}<label>Email address<input type="text" inputmode="email" name="email" autocomplete="email" placeholder="you@example.com"></label><label>Password<div class="password-field"><input type="password" name="password" autocomplete="${register ? 'new-password' : 'current-password'}" placeholder="Enter your password"><button type="button" data-action="toggle-password" aria-label="Show password">Show</button></div></label>${register ? '<p class="field-note">Use a password between 8 and 128 bytes.</p>' : ''}<button type="submit" class="primary full">${register ? 'Create account' : 'Sign in'} <span>→</span></button></form><p class="auth-switch">${register ? 'Already have an account?' : 'Need an account?'} <button class="link-button" data-action="auth-switch">${register ? 'Sign in' : 'Create an account'}</button></p></div></section></div>`;
  $('#auth-form').addEventListener('submit', submitAuth);
}
async function submitAuth(event) {
  event.preventDefault();
  const form = event.currentTarget;
  if (form.dataset.busy) return;
  const data = new FormData(form);
  const email = data.get('email');
  const password = data.get('password');
  const register = state.mode === 'register';
  form.querySelector('.error-message')?.remove();
  form.dataset.busy = 'true';
  const button = form.querySelector('[type="submit"]'); button.disabled = true; button.textContent = register ? 'Creating your account…' : 'Signing you in…';
  try {
    state.user = register ? await api.register(email, password) : await api.signIn(email, password);
    state.error = ''; state.workspaces = []; state.search = ''; form.reset();
    navigate('home'); toast(register ? 'Welcome to MultiWorks. Make yourself at home!' : 'Welcome back!');
  } catch (error) {
    if (error.accountCreated) { state.mode = 'login'; state.error = error.message; renderAuth(); $('#auth-form [name="email"]').value = email; }
    else form.insertAdjacentHTML('afterbegin', errorBox(error.message));
  } finally {
    delete form.dataset.busy; button.disabled = false; button.textContent = register ? 'Create account →' : 'Sign in →';
    if (!form.isConnected) form.querySelector('[name="password"]').value = '';
  }
}

function shell(content, section = 'home') {
  const name = state.user.email.split('@')[0];
  app.innerHTML = `<div class="app-layout"><aside class="sidebar">${brand}<div class="space-label">YOUR SPACE</div><nav aria-label="Main navigation">${[['home', 'Overview'], ['workspaces', 'Workspaces'], ['account', 'My account']].map(([id, title]) => `<a class="nav-link ${section === id ? 'active' : ''}" href="#/${id}" ${section === id ? 'aria-current="page"' : ''}><span>${icons[id]}</span>${title}</a>`).join('')}</nav><div class="sidebar-workspaces"><div class="space-label">WORKSPACES <button class="icon-button" data-action="create-workspace" aria-label="Create workspace">+</button></div>${state.workspaces.slice(0, 6).map((w) => `<a href="#/workspace/${esc(w.id)}" class="workspace-link ${state.workspace?.id === w.id && section === 'workspaces' ? 'current' : ''}"><span class="tiny-square">${esc(initials(w.name))}</span><span>${esc(w.name)}</span></a>`).join('')}${!state.workspaces.length ? '<p class="sidebar-hint">Your shared spaces will live here.</p>' : ''}</div><div class="sidebar-bottom"><a href="#/account" class="user-card"><span class="avatar">${esc(initials(name))}</span><span><strong>${esc(name)}</strong><small>Personal account</small></span><span>↗</span></a></div></aside><div class="main-wrap"><header class="topbar"><span>My space <span class="divider">/</span> <strong>${section === 'home' ? 'Overview' : section === 'account' ? 'My account' : 'Workspaces'}</strong></span><div><span class="today">${date(new Date().toISOString())}</span><button class="avatar avatar-small" data-action="account" aria-label="Open my account">${esc(initials(name))}</button></div></header><main id="main" tabindex="-1">${content}</main><footer>Made for working together.<span>MultiWorks</span></footer></div></div>`;
}
function heading(eyebrow, title, subtitle, action = '') {
  return `<div class="page-heading"><div><p class="eyebrow">${eyebrow}</p><h1>${esc(title)}</h1><p>${esc(subtitle)}</p></div>${action}</div>`;
}
function workspaceCards() {
  const filtered = state.workspaces.filter((w) => w.name.toLowerCase().includes(state.search.toLowerCase()));
  if (!filtered.length && state.workspaces.length) return empty('No matching workspaces', 'Try searching for a different name.');
  if (!filtered.length) return empty('Your first workspace starts here.', 'Create a space for your team, your side project, or your next big idea.', '<button class="primary" data-action="create-workspace">+ Create workspace</button>');
  return `<div class="card-grid">${filtered.map((w, index) => `<a class="workspace-card color-${index % 4}" href="#/workspace/${esc(w.id)}"><div class="card-top"><span class="workspace-icon">${esc(initials(w.name))}</span>${w.created_by === state.user.id ? badge('owner') : '<span class="badge">Joined</span>'}</div><h3>${esc(w.name)}</h3><p>Created ${date(w.created_at)}</p><div class="card-bottom"><span>Open workspace</span><span>↗</span></div></a>`).join('')}<button class="create-card" data-action="create-workspace"><span>+</span><strong>A new space for something great</strong><small>Create workspace</small></button></div>`;
}
function renderHome() {
  const all = route()[0] === 'workspaces';
  document.title = `${all ? 'Workspaces' : 'Home'} · MultiWorks`;
  shell(`${heading(all ? 'ROOM FOR EVERY IDEA' : 'YOUR WORK, ALL TOGETHER', all ? 'Your workspaces' : `Welcome home, ${state.user.email.split('@')[0]}.`, all ? 'Find your people and projects in one place.' : 'Create a workspace or open one to see its projects and people.', '<button class="primary" data-action="create-workspace">+ Create workspace</button>')}<section class="workspace-section"><div class="section-heading"><h2>Your workspaces <span class="count">${state.workspaces.length}</span></h2><label class="search"><span aria-hidden="true">⌕</span><span class="sr-only">Search workspaces</span><input id="workspace-search" placeholder="Find a workspace…" value="${esc(state.search)}"></label></div><div id="workspace-cards">${workspaceCards()}</div></section>`, all ? 'workspaces' : 'home');
  $('#workspace-search').addEventListener('input', (event) => { state.search = event.target.value; $('#workspace-cards').innerHTML = workspaceCards(); });
}
function projectCards() {
  if (!state.projects.length) return empty('Make room for your next project.', 'Projects keep your work focused and your collaborators connected.', '<button class="primary" data-action="create-project">+ Create project</button>');
  return `<div class="card-grid">${state.projects.map((p) => `<a class="project-card" href="#/project/${esc(p.id)}"><div class="card-top"><span class="project-icon">◈</span>${badge(p.member_role)}</div><h3>${esc(p.name)}</h3><p>Updated ${date(p.updated_at)}</p><div class="card-bottom"><span>View project</span><span>↗</span></div></a>`).join('')}</div>`;
}
function memberTable(members, type) {
  return `<div class="table-wrap"><table><thead><tr><th>Person</th><th>Role</th><th>Joined</th><th><span class="sr-only">Manage access</span></th></tr></thead><tbody>${members.map((m) => `<tr><td><div class="person"><span class="avatar">${esc(initials(m.email))}</span><span>${esc(m.email)}${m.user_id === state.user.id ? '<small>You</small>' : ''}</span></div></td><td>${badge(m.role)}</td><td class="muted">${date(m.joined_at)}</td><td><button class="link-button" data-action="edit-${type}-member" data-user="${esc(m.user_id)}">Manage access</button></td></tr>`).join('')}</tbody></table></div>`;
}
function workspaceRole() { return state.workspaceMembers.find((m) => m.user_id === state.user.id)?.role; }
function renderWorkspace() {
  const w = state.workspace;
  const membersTab = route()[2] === 'members';
  document.title = `${w.name} · MultiWorks`;
  shell(`<a class="back-link" href="#/workspaces">← All workspaces</a>${heading('YOUR SHARED SPACE', w.name, `Created ${date(w.created_at)} · ${state.workspaceMembers.length} ${state.workspaceMembers.length === 1 ? 'person' : 'people'}`, `<div class="heading-actions"><button class="${membersTab ? 'primary' : 'secondary'}" data-action="add-workspace-member">+ Add workspace member</button>${!membersTab ? '<button class="primary" data-action="create-project">+ Create project</button>' : ''}</div>`)}<div class="tabs"><a href="#/workspace/${esc(w.id)}" class="${!membersTab ? 'active' : ''}">Projects <span>${state.projects.length}</span></a><a href="#/workspace/${esc(w.id)}/members" class="${membersTab ? 'active' : ''}">People <span>${state.workspaceMembers.length}</span></a><span class="tabs-role">Your role ${badge(workspaceRole())}</span></div>${membersTab ? `<section class="panel"><div class="section-heading"><div><h2>The people behind the work</h2><p>Everyone who belongs to this workspace.</p></div></div>${memberTable(state.workspaceMembers, 'workspace')}<p class="panel-note">The backend checks workspace membership permissions when you submit.</p></section>` : `<div class="section-heading"><h2>Projects</h2><span class="muted small">Showing projects you belong to</span></div>${projectCards()}`}`, 'workspaces');
}
function renderProject() {
  const p = state.project;
  document.title = `${p.name} · MultiWorks`;
  const peopleTab = route()[2] === 'members';
  shell(`<a class="back-link" href="#/workspace/${esc(p.workspace_id)}">← ${esc(state.workspace.name)}</a>${heading('A PLACE TO MAKE PROGRESS', p.name, `Part of ${state.workspace.name}`, '<div class="heading-actions"><button class="secondary" data-action="add-workspace-member">+ Add workspace member</button><button class="primary" data-action="add-project-member">+ Add project member</button></div>')}<div class="project-facts"><div><span class="fact-icon">◈</span><div><small>Your access</small><strong>${esc(p.member_role)}</strong></div></div><div><span class="fact-icon">▦</span><div><small>Created</small><strong>${date(p.created_at)}</strong></div></div><div><span class="fact-icon">↗</span><div><small>Last updated</small><strong>${date(p.updated_at)}</strong></div></div><div><span class="fact-icon">♧</span><div><small>Collaborators</small><strong>${state.members.length} ${state.members.length === 1 ? 'person' : 'people'}</strong></div></div></div><div class="tabs"><a href="#/project/${esc(p.id)}" class="${!peopleTab ? 'active' : ''}">Storyboard</a><a href="#/project/${esc(p.id)}/members" class="${peopleTab ? 'active' : ''}">People <span>${state.members.length}</span></a></div>${peopleTab ? `<section class="panel"><div class="section-heading"><div><h2>Project people</h2><p>A shared project starts with the right people.</p></div>${badge(p.member_role)}</div>${memberTable(state.members, 'project')}<p class="panel-note">The backend checks project membership permissions when you submit.</p></section>` : '<section id="storyboard" aria-label="Project storyboard"></section>'}`, 'workspaces');
  if (!peopleTab) storyboard.load(p.id);
}
function renderAccount() {
  document.title = 'My account · MultiWorks';
  shell(`${heading('MAKE YOURSELF AT HOME', 'My account', 'Your personal details and account access.')}<section class="panel account-panel"><div class="profile-header"><span class="avatar large">${esc(initials(state.user.email))}</span><div><h2>${esc(state.user.email.split('@')[0])}</h2><p>${esc(state.user.email)}</p></div></div><dl class="details-list"><div><dt>Email address</dt><dd>${esc(state.user.email)}</dd></div><div><dt>Member since</dt><dd>${date(state.user.created_at)}</dd></div><div><dt>Account ID</dt><dd class="account-id">${esc(state.user.id)} <button class="link-button" data-action="copy-id">Copy</button></dd></div></dl><p class="field-note">Members are added by email. The backend uses account IDs internally.</p><div class="account-actions"><button class="secondary" data-action="check-connection">Check connection</button><button class="danger-button" data-action="sign-out">Sign out</button></div><p id="connection-result" role="status" class="field-note"></p></section>`, 'account');
}

async function loadPage() {
  const thisVersion = ++version;
  storyboard.dispose();
  modal.close();
  if (!api.authenticated) { state.mode = route()[0] === 'register' ? 'register' : 'login'; renderAuth(); return; }
  state.error = '';
  const page = route();
  state.loading = true;
  if (state.user) shell('<div class="loading-screen" role="status"><span class="spinner"></span> Getting your space ready…</div>', ['workspace', 'project', 'workspaces'].includes(page[0]) ? 'workspaces' : page[0] === 'account' ? 'account' : 'home');
  try {
    if (!state.user) {
      const data = await api.request('me');
      if (thisVersion !== version) return;
      state.user = data.user;
    }
    const data = await api.request('workspaces');
    if (thisVersion !== version) return;
    state.workspaces = data.workspaces; state.workspace = null; state.project = null;
    if (page[0] === 'workspace' && page[1]) {
      const context = { workspaceID: page[1] };
      const [workspace, members, projects] = await Promise.all([api.request('workspace', { context }), api.request('workspace-members', { context }), api.request('projects', { context })]);
      if (thisVersion !== version) return;
      state.workspace = workspace; state.workspaceMembers = members.members; state.projects = projects.projects;
      renderWorkspace();
    } else if (page[0] === 'project' && page[1]) {
      const context = { projectID: page[1] };
      const [project, members] = await Promise.all([api.request('project', { context }), api.request('project-members', { context })]);
      const workspaceContext = { workspaceID: project.workspace_id };
      const [workspace, people] = await Promise.all([api.request('workspace', { context: workspaceContext }), api.request('workspace-members', { context: workspaceContext })]);
      if (thisVersion !== version) return;
      state.project = project; state.members = members.members; state.workspace = workspace; state.workspaceMembers = people.members;
      renderProject();
    } else if (page[0] === 'account') {
      const profile = await api.request('me');
      if (thisVersion !== version) return;
      state.user = profile.user; renderAccount();
    } else renderHome();
  } catch (error) {
    if (thisVersion !== version || !api.authenticated) return;
    const message = error.status === 404 ? 'This space is unavailable. It may not exist, or you may not have access.' : error.message;
    if (!state.user) {
      api.signOut(); state.error = message; renderAuth();
    } else shell(`${heading('LET’S TRY THAT AGAIN', 'We couldn’t open this page.', 'Your work will be here when the connection is ready.')}${errorBox(message)}<div class="error-actions"><button class="primary" data-action="retry">Try again</button><a class="secondary" href="#/home">Go home</a></div>`);
  } finally { if (thisVersion === version) state.loading = false; }
}

function showForm(title, subtitle, fields, submitLabel, submit) {
  modal.innerHTML = `<div class="modal-heading"><span class="modal-icon">✦</span><button type="button" class="icon-button" data-action="close-modal" aria-label="Close dialog">×</button></div><h2 id="modal-title">${title}</h2><p class="modal-subtitle">${subtitle}</p><form id="modal-form" novalidate>${fields}<div id="modal-error" role="alert"></div><div class="modal-actions"><button type="button" class="secondary" data-action="close-modal">Cancel</button><button type="submit" class="primary">${submitLabel} <span>→</span></button></div></form>`;
  modal.showModal();
  const form = $('#modal-form');
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (form.dataset.busy) return;
    form.dataset.busy = 'true';
    const button = form.querySelector('[type="submit"]'); const original = button.innerHTML; button.disabled = true; button.textContent = 'Saving…';
    $('#modal-error').textContent = '';
    const values = Object.fromEntries(new FormData(form));
    try { await submit(values); }
    catch (error) { if (form.isConnected) $('#modal-error').textContent = error.message; }
    finally { delete form.dataset.busy; button.disabled = false; button.innerHTML = original; }
  });
}
function createWorkspace() {
  showForm('A space for your next chapter.', 'Give your workspace a name. You can bring your people in next.', nameField('e.g. Design studio'), 'Create workspace', async ({ name }) => {
    const workspace = await api.request('create-workspace', { body: { name } });
    modal.close(); navigate(`workspace/${workspace.id}`); toast('Your workspace is ready. Let’s make something great.');
  });
}
function createProject() {
  const workspace = state.workspace;
  showForm('Let’s start something.', `Create a project in ${esc(workspace.name)}.`, nameField('e.g. Website launch'), 'Create project', async ({ name }) => {
    const project = await api.request('create-project', { context: { workspaceID: workspace.id }, body: { name } });
    modal.close(); navigate(`project/${project.id}`); toast('Project created. You’re ready to collaborate.');
  });
}
function memberForm(type, userID, { email = '' } = {}) {
  const workspace = state.workspace;
  const project = state.project;
  const isProject = type === 'project';
  const roles = isProject ? ['viewer', 'editor'] : ['member', 'admin'];
  const member = (isProject ? state.members : state.workspaceMembers).find((m) => m.user_id === userID);
  const suggestedPeople = state.workspaceMembers.map((m) => `<option value="${esc(m.email)}"></option>`).join('');
  const personField = `<label>Member email<input type="text" name="email" inputmode="email" value="${esc(member?.email || email)}" list="member-suggestions" autocomplete="off" placeholder="person@example.com"><datalist id="member-suggestions">${suggestedPeople}</datalist></label><p class="field-note">Enter an email address. The backend resolves it to the account ID and checks validation and permissions.${isProject ? ` Project members must belong to the workspace first. <a class="link-button" href="#/workspace/${esc(workspace.id)}/members">Open workspace people →</a> <button type="button" class="secondary" data-action="open-workspace-member">Add workspace member</button>` : ''}</p>`;
  const roleField = `<label>Role<input type="text" name="role" value="${esc(member?.role || roles[0])}" list="role-suggestions" autocomplete="off"><datalist id="role-suggestions">${roles.map((role) => `<option value="${role}"></option>`).join('')}</datalist></label><p class="field-note">Suggested roles: ${roles.join(', ')}. Other values can be submitted to test backend validation.</p>`;
  showForm(userID ? 'Manage their access.' : isProject ? 'Add project member' : 'Add workspace member', userID ? `Update access for ${esc(member?.email || 'this member')}.` : `Add someone to ${esc(isProject ? project.name : workspace.name)}.`, personField + roleField, userID ? 'Save access' : 'Add member', async (values) => {
    await api.request(isProject ? 'add-project-member' : 'add-workspace-member', {
      context: isProject ? { projectID: project.id } : { workspaceID: workspace.id },
      body: { email: values.email, role: values.role },
    });
    modal.close(); await loadPage(); toast(userID ? 'Access updated.' : 'Member added.');
  });
}

app.addEventListener('click', async (event) => {
  const boardButton = event.target.closest('[data-board-action]');
  if (boardButton) { await storyboard.handle(boardButton); return; }
  const button = event.target.closest('[data-action]');
  if (!button) return;
  const action = button.dataset.action;
  if (action === 'auth-switch') { if ($('#auth-form')?.dataset.busy) return; state.error = ''; state.mode = state.mode === 'login' ? 'register' : 'login'; location.hash = `#/${state.mode}`; }
  else if (action === 'toggle-password') { const input = $('[name="password"]'); input.type = input.type === 'password' ? 'text' : 'password'; button.textContent = input.type === 'password' ? 'Show' : 'Hide'; button.setAttribute('aria-label', `${button.textContent} password`); }
  else if (action === 'sign-out') { api.signOut(); state.user = null; state.workspaces = []; state.error = ''; version++; navigate('login'); toast('You’ve signed out. See you soon.'); }
  else if (action === 'account') navigate('account');
  else if (action === 'retry') loadPage();
  else if (action === 'create-workspace') createWorkspace();
  else if (action === 'create-project') createProject();
  else if (action === 'add-workspace-member' || action === 'edit-workspace-member') memberForm('workspace', button.dataset.user);
  else if (action === 'add-project-member' || action === 'edit-project-member') memberForm('project', button.dataset.user);
  else if (action === 'copy-id') {
    try { await navigator.clipboard.writeText(state.user.id); toast('Account ID copied.'); }
    catch { toast('Copy the account ID shown on your profile.'); }
  } else if (action === 'check-connection') {
    button.disabled = true; const result = $('#connection-result'); result.textContent = 'Checking connection…';
    try { const data = await api.request('health'); result.textContent = data.status === 'ok' ? 'Everything looks good. Your workspace is connected.' : 'The service needs attention. Please try again.'; }
    catch (error) { result.textContent = error.message; }
    finally { button.disabled = false; }
  }
});
modal.addEventListener('click', (event) => {
  const action = event.target.closest('[data-action]')?.dataset.action;
  if ($('#modal-form')?.dataset.busy) return;
  if (action === 'close-modal') modal.close();
  else if (action === 'open-workspace-member') {
    const email = $('#modal-form [name="email"]').value;
    modal.close();
    memberForm('workspace', undefined, { email });
  }
});
modal.addEventListener('cancel', (event) => { if ($('#modal-form')?.dataset.busy) event.preventDefault(); });
app.addEventListener('error', (event) => {
  if (event.target.matches?.('.shot-image')) { event.target.hidden = true; event.target.nextElementSibling.hidden = false; }
}, true);
window.addEventListener('hashchange', loadPage);
loadPage();
