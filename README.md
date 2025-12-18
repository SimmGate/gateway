**Simmgate-gateway**

SimmGate Gateway is a high-performance, production-grade Go service that proxies LLM API requests, adds intelligent caching, supports streaming responses, and exposes profiling/metrics for observability.

This gateway implements Tier 1: Exact Caching, SSE streaming, LLM client with retries, and full HTTP middleware stack.
It is the first component of the broader SimmGate semantic caching architecture.

Features
/v1/chat/completions API

OpenAI-compatible chat request handler

Supports both:

Non-streaming (JSON response)

Streaming (SSE / text/event-stream)

Fully HTTP/1.1 compliant streaming using flush

Tier-1 Exact Cache

Normalizes request

Generates a reproducible SHA-256 cache key

Memory or Redis backend

Transparent cache hit/miss instrumentation

Caches non-stream responses for fast replay

LLM Client (internal/llm)

Built-in support for:

Chat completions

SSE streaming

Automatic retries with backoff

Converts OpenAI responses → internal response format

Separates provider logic from gateway logic (clean architecture)

Streaming Engine

Converts upstream SSE chunks into OpenAI-style delta tokens

Flushes each chunk to the client for real-time output

Emits data: [DONE] sentinel

Middleware Stack

Timeout protection

Max body size

Structured logging (Zap)

Request ID

Prometheus metrics

Panic recovery

Observability

pprof routes built-in:

/debug/pprof/


Use with:

go tool pprof "http://localhost:8080/debug/pprof/profile?seconds=5"

Architecture Overview
Client - Gateway (Go) - Cache (Memory/Redis) - LLM Upstream (OpenAI/etc)
                     - Semantic Tier Coming in Sprint 3

Handler Logic
Non-Streaming
parse request → build cache key → lookup → 
  hit: return cached response
  miss: call LLM → cache response → return JSON

Streaming
parse request - call LLM stream - forward SSE chunks - DONE sentinel

Installation
git clone https://github.com/<you>/simmgate-gateway
cd simmgate-gateway

Configuration

Environment variables:

Variable	Description	Default
LLM_API_KEY	Upstream LLM API key	(required)
LLM_BASE_URL	LLM API base URL	https://api.openai.com

CACHE_BACKEND	memory or redis	memory
REDIS_ADDR	Redis address	127.0.0.1:6379
PORT	Gateway port	8080
GATEWAY_VERSION	Cache namespace version	v1
Example .env
LLM_API_KEY=sk-example
CACHE_BACKEND=memory
PORT=8080
GATEWAY_VERSION=v1


Load it in zsh/bash:

set -a
source .env
set +a

Running the Gateway
go run ./cmd/gateway

Testing the API
Non-streaming:
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}'

Streaming:
curl -N http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4","stream":true,"messages":[{"role":"user","content":"hello"}]}'

Tests

The project includes full tests for:

LLM Client

Request validation

Success mapping

SSE streaming decoding

Retry behavior

Chat Handler

Non-stream caching path

Streaming SSE output

Correct number of LLM calls

Cache population behavior

Run tests:

go test ./...
