import os
import psycopg
import httpx

DB_URL = os.environ.get("DATABASE_URL", "postgresql://postgres:postgres@localhost:5432/mvp")
API_KEY = os.environ.get("OPENROUTER_API_KEY", "")
EMBED_MODEL = os.environ.get("OPENROUTER_EMBED_MODEL", "openai/text-embedding-3-small")

DOCS = {
    "ARCHITECTURE.md": open("ARCHITECTURE_OPENROUTER.md").read()[:6000],
    "MVP_MILESTONE.md": open("MVP_MILESTONE.md").read()[:3000],
}


def chunk(text, size=500, overlap=50):
    words = text.split()
    out, i = [], 0
    while i < len(words):
        out.append(" ".join(words[i : i + size]))
        i += size - overlap
    return out


def main():
    texts, sources = [], []
    for src, text in DOCS.items():
        for ch in chunk(text):
            texts.append(ch)
            sources.append(src)
    if not API_KEY:
        print("no OPENROUTER_API_KEY, storing chunks without embeddings")
        vecs = [None] * len(texts)
    else:
        r = httpx.post(
            "https://openrouter.ai/api/v1/embeddings",
            headers={"Authorization": f"Bearer {API_KEY}"},
            json={"model": EMBED_MODEL, "input": texts},
            timeout=60,
        )
        r.raise_for_status()
        vecs = [d["embedding"] for d in r.json()["data"]]
    with psycopg.connect(DB_URL) as c:
        for src, t, v in zip(sources, texts, vecs):
            c.execute(
                "INSERT INTO ai_svc.knowledge_chunks(source, chunk_text, embedding) VALUES (%s,%s,%s)",
                (src, t, str(v) if v else None),
            )
        c.commit()
    print(f"seeded {len(texts)} chunks")


if __name__ == "__main__":
    main()
