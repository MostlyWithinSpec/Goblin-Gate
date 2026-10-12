// Error body of every API error (internal/api ErrorBody). Config types live
// next to their API calls: objects.ts, network.ts, policy.ts, stats.ts.
export interface APIError {
  error: {
    code: string
    message: string
    details?: unknown
  }
}
