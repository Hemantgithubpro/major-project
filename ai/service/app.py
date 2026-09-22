import json
import os
import time
import uuid

import httpx
import psycopg
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from pydantic import BaseModel

app = FastAPI(title="genai-service")
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["GET", "POST", "OPTIONS"],
    allow_headers=["Content-Type", "X-Correlation-ID"],
)

DB_URL = os.environ.get("DATABASE_URL", "postgresql://postgres:postgres@localhost:5432/mvp")
API_KEY = os.environ.get("OPENROUTER_API_KEY", "")
GEN_MODEL = os.environ.get("OPENROUTER_GEN_MODEL", "openai/gpt-4o-mini")
FALLBACKS = [m for m in os.environ.get("OPENROUTER_FALLBACK_MODELS", "").split(",") if m]
EMBED_MODEL = os.environ.get("OPENROUTER_EMBED_MODEL", "openai/text-embedding-3-small")
REFERER = os.environ.get("OPENROUTER_HTTP_REFERER", "")
TITLE = os.environ.get("OPENROUTER_APP_TITLE", "mvp-reliability")
OFFLINE_MOCK = os.environ.get("GENAI_OFFLINE_MOCK", "false").lower() == "true"
PROMPT_VERSION = "v1"

SYSTEM_PROMPT = (
    "You are an SRE assistant. Use ONLY the evidence provided. "
    "Never invent a metric, timestamp, or service name. "
    "Label every claim FACT, PREDICTION, INFERENCE, or RECOMMENDATION. "
    "If evidence is insufficient, say so explicitly."
)


def db():
    return psycopg.connect(DB_URL)


def evidence_bundle(incident_id: str) -> dict:
    with db() as c:
        inc = c.execute(
            "SELECT id::text, scenario, transaction_id::text, status, started_at::text, resolved_at::text FROM ai_svc.incidents WHERE id=%s",
            (incident_id,),
        ).fetchone()
        if not inc:
            return {}
        ev = c.execute(
            "SELECT kind, data, captured_at::text FROM ai_svc.incident_evidence WHERE incident_id=%s ORDER BY id",
            (incident_id,),
        ).fetchall()
        steps = []
        if inc[2]:
            steps = c.execute(
                "SELECT step_name, step_order, status, error FROM saga_svc.saga_steps WHERE saga_id=(SELECT id FROM saga_svc.sagas WHERE transaction_id=%s) ORDER BY step_order",
                (inc[2],),
            ).fetchall()
    return {
        "incident": {"id": inc[0], "scenario": inc[1], "transaction_id": inc[2], "status": inc[3], "started_at": inc[4], "resolved_at": inc[5]},
        "evidence": [{"kind": k, "data": d, "at": t} for k, d, t in ev],
        "saga_steps": [{"step": s[0], "order": s[1], "status": s[2], "error": s[3]} for s in steps],
    }


def retrieve_chunks(query: str, k: int = 8) -> list:
    try:
        vec = embed([query])[0]
        with db() as c:
            rows = c.execute(
                "SELECT source, chunk_text FROM ai_svc.knowledge_chunks ORDER BY embedding <=> %s::vector LIMIT %s",
                (str(vec), k),
            ).fetchall()
        return [{"source": r[0], "text": r[1]} for r in rows]
    except Exception:
        return []


def embed(texts: list) -> list:
    r = httpx.post(
        "https://openrouter.ai/api/v1/embeddings",
        headers={"Authorization": f"Bearer {API_KEY}"},
        json={"model": EMBED_MODEL, "input": texts},
        timeout=30,
    )
    r.raise_for_status()
    return [d["embedding"] for d in r.json()["data"]]


def chat(messages: list) -> tuple[str, str, int, int]:
    headers = {"Authorization": f"Bearer {API_KEY}"}
    if REFERER:
        headers["HTTP-Referer"] = REFERER
    if TITLE:
        headers["X-Title"] = TITLE
    last_err = "no key" if not API_KEY else ""
    for model in [GEN_MODEL] + FALLBACKS:
        for attempt in range(3):
            try:
                r = httpx.post(
                    "https://openrouter.ai/api/v1/chat/completions",
                    headers=headers,
                    json={"model": model, "messages": messages, "temperature": 0.2, "max_tokens": 2000},
                    timeout=60,
                )
                r.raise_for_status()
                j = r.json()
                usage = j.get("usage", {})
                return j["choices"][0]["message"]["content"], model, usage.get("prompt_tokens", 0), usage.get("completion_tokens", 0)
            except Exception as e:
                last_err = str(e)
                time.sleep(2 ** attempt)
    raise RuntimeError(f"all models failed: {last_err}")


def mock_report(bundle: dict) -> str:
    inc = bundle.get("incident", {})
    steps = "\n".join(f"- {s['step']}: {s['status']}" for s in bundle.get("saga_steps", []))
    return (
        f"# Incident Report (OFFLINE MOCK)\n\n## Summary\n"
        f"FACT: incident {inc.get('id')} scenario {inc.get('scenario')} transaction {inc.get('transaction_id')}.\n\n"
        f"## Timeline\n{steps}\n\n## Root Cause\nINFERENCE: risk check failure triggered saga compensation.\n\n"
        f"## Evidence\nFACT: saga steps recorded in saga_svc.saga_steps.\n\n"
        f"## Actions Taken\nFACT: payment refunded, inventory released.\n\n"
        f"## Recommendation\nRECOMMENDATION: investigate risk-service outage cause."
    )


class GenReq(BaseModel):
    incident_id: str


class QueryReq(BaseModel):
    question: str
    incident_id: str | None = None


@app.get("/healthz")
def healthz():
    return {"status": "ok"}


@app.post("/generate-postmortem")
def generate(req: GenReq):
    bundle = evidence_bundle(req.incident_id)
    if not bundle:
        return JSONResponse({"error": "incident not found"}, status_code=404)
    chunks = retrieve_chunks(bundle["incident"].get("scenario", "") + " " + (bundle["incident"].get("transaction_id") or ""))
    if OFFLINE_MOCK or not API_KEY:
        md, model, pt, ct = mock_report(bundle), "offline-mock", 0, 0
        cached = True
    else:
        user = f"<evidence>{json.dumps(bundle)}</evidence>\n<docs>{json.dumps(chunks)}</docs>\nWrite the incident report with sections: Summary, Timeline, Root Cause, Evidence, Actions Taken, Recommendation."
        try:
            md, model, pt, ct = chat([{"role": "system", "content": SYSTEM_PROMPT}, {"role": "user", "content": user}])
            cached = False
        except Exception as e:
            md, model, pt, ct = mock_report(bundle) + f"\n\n(LLM failed, served mock: {e})", "offline-mock", 0, 0
            cached = True
    rid = str(uuid.uuid4())
    with db() as c:
        c.execute(
            "INSERT INTO ai_svc.generated_reports(id, incident_id, markdown, model, prompt_version, evidence_bundle_id, prompt_tokens, completion_tokens, cached) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s)",
            (rid, req.incident_id, md, model, PROMPT_VERSION, req.incident_id, pt, ct, cached),
        )
        c.commit()
    return {"report_id": rid, "incident_id": req.incident_id, "model": model, "cached": cached, "markdown": md}


@app.post("/rag-query")
def query(req: QueryReq):
    bundle = evidence_bundle(req.incident_id) if req.incident_id else {}
    chunks = retrieve_chunks(req.question)
    if OFFLINE_MOCK or not API_KEY:
        return {"answer": "OFFLINE MOCK: evidence recorded, LLM unavailable.", "evidence": chunks, "cached": True}
    user = f"<evidence>{json.dumps(bundle)}</evidence>\n<docs>{json.dumps(chunks)}</docs>\nQuestion: {req.question}"
    try:
        text, model, _, _ = chat([{"role": "system", "content": SYSTEM_PROMPT}, {"role": "user", "content": user}])
        return {"answer": text, "model": model, "evidence": chunks, "cached": False}
    except Exception as e:
        return JSONResponse({"error": str(e)}, status_code=503)


@app.get("/incidents/{iid}/report")
def latest(iid: str):
    with db() as c:
        row = c.execute(
            "SELECT markdown, model, cached, generated_at::text FROM ai_svc.generated_reports WHERE incident_id=%s ORDER BY generated_at DESC LIMIT 1",
            (iid,),
        ).fetchone()
    if not row:
        return JSONResponse({"error": "no report"}, status_code=404)
    return {"markdown": row[0], "model": row[1], "cached": row[2], "at": row[3]}
