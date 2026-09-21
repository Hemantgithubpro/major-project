# ADR 0011 — OpenRouter Cloud LLM over Local Ollama

- Status: Accepted (supersedes ADR 0009 sequential-demo-mode).
- Date: 2026-09-20.

## Context

Master design ran GenAI on local Ollama (small quantised model + `nomic-embed-text`,
sequential demo mode) to preserve the $0 constraint. Team now has decent OpenRouter
token budget. Control plane is Go; AI sidecars stay Python/FastAPI.

## Decision

`genai-service` calls **OpenRouter** (`/chat/completions` + `/embeddings`) instead of Ollama.
Generation: `gpt-4o-mini` / `gemini-flash` class primary, `:free` models
(`llama-3.1-8b-instruct:free`, `mistral-7b-instruct:free`) as automatic fallback chain.
Embeddings: `text-embedding-3-small` (1536-d) into existing pgvector (`vector(1536)`, HNSW).
No Ollama container, no sequential demo mode, no local GPU/RAM sizing for LLMs.

## Consequences

- Good: frees ~1.5–2.5 GB RAM; fully live demo (chaos → report, one session);
  larger context windows for evidence bundles; zero local-model ops.
- Bad: needs network + API key on demo day; small metered cost (bounded by
  monthly/per-report caps + token logging in `generated_reports`).
- Guardrails unchanged: LLM text/JSON only, deterministic controller alone acts,
  both read the same evidence tables, model+prompt+evidence recorded per report.
- Contingency: `:free` fallback chain, then `GENAI_OFFLINE_MOCK` cached report
  (labeled as fallback, never presented as live).
