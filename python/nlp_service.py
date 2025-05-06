# nlp_service.py
import os
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from typing import List
import openai
import uvicorn
from dotenv import load_dotenv
from pymongo import MongoClient
from datetime import datetime, timedelta

load_dotenv()
openai.api_key = os.getenv("OPENAI_API_KEY")

# MongoDB setup
MONGO_URI = os.getenv("MONGO_URI", "mongodb://localhost:27017")
client = MongoClient(MONGO_URI)
db = client["news_ai_agent"]
collection = db["raw_articles"]

app = FastAPI()

class AnalyzeRequest(BaseModel):
    title: str
    content: str
    topic: str
    published_at: str  # ISO format

class AnalyzeResponse(BaseModel):
    topic: str
    impact_level: str
    tags: List[str]

class ArticleEmbedding(BaseModel):
    title: str
    content: str
    embedding: List[float]

# --- OpenAI Embedding ---
def get_embedding(text: str) -> List[float]:
    response = openai.Embedding.create(
        model="text-embedding-3-small",
        input=text
    )
    return response['data'][0]['embedding']

# --- Save article with embedding ---
def store_article_with_embedding(title: str, content: str, metadata: dict):
    embedding = get_embedding(title + "\n" + content)
    doc = {
        "title": title,
        "content": content,
        "embedding": embedding,
        "fetched_at": datetime.utcnow(),
        **metadata
    }
    collection.insert_one(doc)

# --- Cosine similarity ---
def cosine_similarity(v1, v2):
    from numpy import dot
    from numpy.linalg import norm
    return dot(v1, v2) / (norm(v1) * norm(v2))

# --- Retrieve similar articles ---
def get_similar_articles(title: str, content: str, top_k: int = 3) -> List[str]:
    import numpy as np
    target_emb = np.array(get_embedding(title + "\n" + content))
    results = []

    for doc in collection.find({"embedding": {"$exists": True}}):
        emb = np.array(doc["embedding"])
        sim = cosine_similarity(target_emb, emb)
        results.append((sim, doc))

    results.sort(key=lambda x: x[0], reverse=True)
    return [f"[{d['published_at']}] {d['title']}" for _, d in results[:top_k]]

@app.post("/analyze", response_model=AnalyzeResponse)
def analyze_news(req: AnalyzeRequest):
    try:
        similar_context = get_similar_articles(req.title, req.content)
        context = "\n".join(similar_context)

        prompt = f"""
Here are recent events semantically similar to the new article:
{context}

Now, analyze the following article and return JSON with:
- topic: main subject
- impact_level: low, medium, or high
- tags: list of financial keywords related

Title: {req.title}
Content: {req.content}
"""

        response = openai.ChatCompletion.create(
            model="gpt-4",
            messages=[
                {"role": "system", "content": "You are a macroeconomic analysis assistant."},
                {"role": "user", "content": prompt}
            ],
            temperature=0.3,
            max_tokens=500
        )

        content = response.choices[0].message.content
        result = eval(content)

        return AnalyzeResponse(**result)
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

if __name__ == "__main__":
    uvicorn.run("nlp_service:app", host="0.0.0.0", port=9000, reload=True)
