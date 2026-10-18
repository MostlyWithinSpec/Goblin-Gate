import { APIError } from './types'

// APIRequestError is thrown for every non-2xx answer: message is the
// server's message, code its error code, details e.g. the validation errors
// of a failed commit ([{path, message}]).
export class APIRequestError extends Error {
  status: number
  code: string
  details: unknown
  constructor(message: string, status: number, code: string, details: unknown) {
    super(message)
    this.name = 'APIRequestError'
    this.status = status
    this.code = code
    this.details = details
  }
}

// Fetch wrapper with CSRF token handling and error handling
export const apiClient = async <T>(
  url: string,
  options?: RequestInit
): Promise<T> => {
  // Get CSRF token from cookie if available
  const getCSRFToken = (): string | null => {
    try {
      const cookies = document.cookie.split(';');
      for (const cookie of cookies) {
        const [name, value] = cookie.trim().split('=');
        if (name === 'gg_csrf') {
          return value;
        }
      }
    } catch (e) {
      // Silently fail if cookies are not accessible
    }
    return null;
  };

  const csrfToken = getCSRFToken();
  const defaultHeaders: HeadersInit = {
    'Content-Type': 'application/json',
  };

  if (csrfToken) {
    defaultHeaders['X-CSRF-Token'] = csrfToken;
  }

  const config: RequestInit = {
    ...options,
    headers: {
      ...defaultHeaders,
      ...options?.headers,
    },
  };

  const response = await fetch(url, config);

  // Handle 401 Unauthorized
  if (response.status === 401) {
    // Redirect to login page
    window.location.href = '/login';
    throw new Error('Unauthorized');
  }

  // Handle errors: {error: {code, message, details}}; details is the
  // underlying reason (e.g. which field was wrong), shown after the message.
  if (!response.ok) {
    let message = response.statusText || `HTTP ${response.status}`
    let code = ''
    let details: unknown
    try {
      const body = (await response.json()) as APIError
      if (body?.error?.message) {
        message = body.error.message
        code = body.error.code
        details = body.error.details
        if (typeof details === 'string' && details) message += `: ${details}`
      }
    } catch {
      // not JSON: keep the status text
    }
    throw new APIRequestError(message, response.status, code, details)
  }

  // 204 No Content (e.g. DELETE) has no body to parse.
  if (response.status === 204) return undefined as T

  // Return parsed JSON data
  return response.json();
};