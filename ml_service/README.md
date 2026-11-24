# Python ML service

The service exposes BGE-M3 embeddings and BGE Reranker v2 M3 to the Go application.

## API

- `GET /health` — readiness check. It returns healthy only after both models are loaded.
- `POST /v1/embeddings` — accepts `{"texts":["..."]}` and returns one 1024-dimensional vector per text.
- `POST /v1/rerank` — accepts `{"query":"...","passages":["..."]}` and returns one relevance score per passage. Sort scores in descending order.

The API is intended for the internal Docker network and is not published on the host.

## Runtime

The Compose image uses CPU-only PyTorch by default. The first start downloads both models from Hugging Face and can take several minutes; the `hf_cache` volume keeps the weights between restarts.

Configuration:
- `MODEL_DEVICE=cpu` — default runtime.
- `EMBEDDING_BATCH_SIZE=16` — internal BGE-M3 batch size for one inference call.
- `DYNAMIC_BATCH_MAX_SIZE=128` — maximum number of texts collected from concurrent HTTP requests for one inference call.
- `DYNAMIC_BATCH_MAX_WAIT_MS=150` — maximum time the worker waits for more texts before flushing a partial batch.
- `DYNAMIC_BATCH_QUEUE_SIZE=4096` — maximum number of pending texts; requests wait for capacity and fail with HTTP 503 if the queue stays full for 10 seconds.
- `MODEL_MAX_LENGTH=512` — maximum input token length for product text.

Dynamic batching combines texts from multiple Go requests. The model still performs one inference call at a time because embeddings and reranking share the same device and are protected by a lock. The dynamic batch size and the model's internal `EMBEDDING_BATCH_SIZE` are separate settings.

The current Dockerfile is CPU-only. CUDA requires a separate image with a compatible CUDA-enabled PyTorch build; setting `MODEL_DEVICE=cuda` in the default image will fail.

## Product indexing

The Go import pipeline sends product title, category, info and description in batches of 64. Prices are deliberately kept as structured SQL fields and are not included in the embedding text. If product text changes, its old vector is cleared and regenerated during the next catalogue import.

Existing rows do not receive vectors automatically just by starting the application. Run the normal `POST /parse` import once after deploying this branch to populate the embeddings.
