import { APIError } from './types'

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
    try {
      const body = (await response.json()) as APIError
      if (body?.error?.message) {
        message = body.error.message
        const d = body.error.details
        if (typeof d === 'string' && d) message += `: ${d}`
      }
    } catch {
      // not JSON: keep the status text
    }
    throw new Error(message)
  }

  // Return parsed JSON data
  return response.json();
};