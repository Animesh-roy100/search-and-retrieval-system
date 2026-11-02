/** Types mirroring the Go query service (cmd/query) response shapes. */

export interface Source {
  marker: string; // "doc_1"
  doc_id: string;
  origin: string; // source system, e.g. "postgres"
  text: string;
}

export interface SearchResponse {
  query: string;
  results: Source[] | null;
  latency_ms: number;
}

export interface Citation {
  marker: string;
  doc_id: string;
  source?: string;
  score?: number;
}

export interface AskResponse {
  answer: string;
  citations?: Citation[] | null;
  faithfulness?: number;
  unsupported?: string[] | null;
  model?: string;
  latency_ms?: number;
  warning?: string;
  // graceful-degradation / cache fields the backend may add
  degraded?: string;
  results?: Source[] | null;
  cached?: boolean;
  semantic?: boolean;
  semantic_score?: number;
}

export interface RequestBody {
  query: string;
  filters?: Record<string, string>;
  top_k?: number;
}
