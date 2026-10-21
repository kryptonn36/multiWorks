import { endpoints, requestPath } from './endpoints.js';

export class ApiError extends Error {
  constructor(message, status = 0) { super(message); this.status = status; }
}

// Keep the route definitions and same-origin proxy used by the original frontend.
export function createClient({ fetcher = globalThis.fetch, storage, onUnauthorized = () => {} } = {}) {
  let token = '';
  try { token = storage?.getItem('multiworks.token') || ''; } catch { /* In-memory sessions still work. */ }
  function setToken(value) {
    token = value;
    try { value ? storage?.setItem('multiworks.token', value) : storage?.removeItem('multiworks.token'); } catch { /* Storage is optional. */ }
  }
  async function request(id, { context = {}, body } = {}) {
    const endpoint = endpoints.find((item) => item.id === id);
    if (!endpoint) throw new Error('Unknown operation');
    const headers = { Accept: 'application/json' };
    const requestToken = token;
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (!endpoint.public && requestToken) headers.Authorization = `Bearer ${requestToken}`;
    let response;
    try {
      response = await fetcher(`/backend${requestPath(endpoint, context)}`, {
        method: endpoint.method, headers,
        ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
        signal: AbortSignal.timeout(35000),
      });
    } catch (error) {
      throw new ApiError(error.name === 'TimeoutError' ? 'This is taking too long. Please try again.' : 'Unable to connect. Please check that the application is running.');
    }
    if (response.status === 401 && !endpoint.public) {
      // An older request must not clear a newly signed-in session.
      if (requestToken === token) {
        setToken(''); onUnauthorized();
      }
      throw new ApiError('Your session has expired. Please sign in again.', 401);
    }
    let data;
    try { data = await response.json(); } catch { throw new ApiError('The server returned an unexpected response. Please try again.', response.status); }
    if (!response.ok) {
      throw new ApiError(response.status === 502 ? 'The service is unavailable. Please try again shortly.' : data.error || 'Something went wrong. Please try again.', response.status);
    }
    return data;
  }
  async function signIn(email, password) {
    const data = await request('login', { body: { email, password } });
    if (!data.access_token || !data.user) throw new ApiError('Could not complete sign in. Please try again.');
    setToken(data.access_token);
    return data.user;
  }
  async function register(email, password) {
    await request('register', { body: { email, password } });
    try { return await signIn(email, password); }
    catch (error) {
      const failure = new ApiError(`Your account was created, but sign in failed. Please sign in with your new account. ${error.message}`, error.status);
      failure.accountCreated = true;
      throw failure;
    }
  }
  return { request, signIn, register, signOut: () => setToken(''), get authenticated() { return Boolean(token); } };
}
