import os
import threading
from contextlib import asynccontextmanager
from typing import Any

import numpy as np
import torch
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field, field_validator
from FlagEmbedding import BGEM3FlagModel, FlagReranker


class EmbeddingRequest(BaseModel):
    texts: list[str] = Field(min_length=1, max_length=128)

    @field_validator("texts")
    @classmethod
    def validate_texts(cls, values: list[str]) -> list[str]:
        if any(not text.strip() for text in values):
            raise ValueError("texts must not contain empty strings")
        if any(len(text) > 20000 for text in values):
            raise ValueError("each text must contain at most 20000 characters")
        return values


class RerankRequest(BaseModel):
    query: str = Field(min_length=1, max_length=4000)
    passages: list[str] = Field(min_length=1, max_length=128)

    @field_validator("passages")
    @classmethod
    def validate_passages(cls, values: list[str]) -> list[str]:
        if any(not text.strip() for text in values):
            raise ValueError("passages must not contain empty strings")
        if any(len(text) > 20000 for text in values):
            raise ValueError("each passage must contain at most 20000 characters")
        return values


class ModelRuntime:
    def __init__(self) -> None:
        requested_device = os.getenv("MODEL_DEVICE", "auto").lower()
        if requested_device == "auto":
            self.device = "cuda" if torch.cuda.is_available() else "cpu"
        elif requested_device == "cuda" and not torch.cuda.is_available():
            raise RuntimeError("MODEL_DEVICE=cuda, but CUDA is not available")
        else:
            self.device = requested_device

        if self.device not in {"cpu", "cuda"}:
            raise RuntimeError(f"unsupported MODEL_DEVICE: {self.device}")

        use_fp16 = self.device == "cuda"
        embedding_name = os.getenv("EMBEDDING_MODEL", "BAAI/bge-m3")
        reranker_name = os.getenv("RERANKER_MODEL", "BAAI/bge-reranker-v2-m3")

        self.embedding_batch_size = int(os.getenv("EMBEDDING_BATCH_SIZE", "16"))
        self.max_length = int(os.getenv("MODEL_MAX_LENGTH", "512"))
        devices = [self.device]

        self.embedder = BGEM3FlagModel(
            embedding_name,
            use_fp16=use_fp16,
            devices=devices,
        )
        self.reranker = FlagReranker(
            reranker_name,
            use_fp16=use_fp16,
            devices=devices,
            query_max_length=256,
            passage_max_length=self.max_length,
        )
        self.lock = threading.Lock()
        self.embedding_model = embedding_name
        self.reranker_model = reranker_name

    def embed(self, texts: list[str]) -> list[list[float]]:
        with self.lock:
            encoded = self.embedder.encode(
                texts,
                batch_size=self.embedding_batch_size,
                max_length=self.max_length,
                return_dense=True,
                return_sparse=False,
                return_colbert_vecs=False,
            )
        vectors = np.asarray(encoded["dense_vecs"], dtype=np.float32)
        if vectors.ndim != 2 or vectors.shape[1] != 1024:
            raise RuntimeError(f"unexpected embedding shape: {vectors.shape}")
        return vectors.tolist()

    def rerank(self, query: str, passages: list[str]) -> list[float]:
        pairs = [[query, passage] for passage in passages]
        with self.lock:
            raw_scores = self.reranker.compute_score(pairs, normalize=False)
        scores = np.asarray(raw_scores, dtype=np.float32).reshape(-1)
        if len(scores) != len(passages):
            raise RuntimeError("reranker returned an unexpected number of scores")
        return [float(score) for score in scores]


@asynccontextmanager
async def lifespan(app: FastAPI):
    # Load both models once. The API becomes healthy only after initialization.
    app.state.runtime = ModelRuntime()
    yield


app = FastAPI(title="Site Parser ML Service", version="1.0.0", lifespan=lifespan)


@app.get("/health")
def health() -> dict[str, Any]:
    runtime = getattr(app.state, "runtime", None)
    if runtime is None:
        raise HTTPException(status_code=503, detail="models are not loaded")
    return {
        "status": "ok",
        "device": runtime.device,
        "embedding_model": runtime.embedding_model,
        "reranker_model": runtime.reranker_model,
        "embedding_dimensions": 1024,
    }


@app.post("/v1/embeddings")
def embeddings(request: EmbeddingRequest) -> dict[str, Any]:
    runtime = getattr(app.state, "runtime", None)
    if runtime is None:
        raise HTTPException(status_code=503, detail="models are not loaded")
    try:
        return {"embeddings": runtime.embed(request.texts)}
    except Exception as exc:
        raise HTTPException(status_code=500, detail=f"embedding failed: {exc}") from exc


@app.post("/v1/rerank")
def rerank(request: RerankRequest) -> dict[str, Any]:
    runtime = getattr(app.state, "runtime", None)
    if runtime is None:
        raise HTTPException(status_code=503, detail="models are not loaded")
    try:
        return {"scores": runtime.rerank(request.query, request.passages)}
    except Exception as exc:
        raise HTTPException(status_code=500, detail=f"reranking failed: {exc}") from exc
