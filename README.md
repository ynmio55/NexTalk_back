# NexTalk Backend

Backend API for NexTalk — a self-hosted chat app for a small private group.

## Stack
- Go 1.23+
- PostgreSQL
- REST API
- WebSocket realtime messaging
- JWT authentication

## Quick start

1. Copy `.env.example` to `.env`
2. Fill in PostgreSQL credentials
3. Run:

```bash
go mod tidy
go run ./cmd/server
```

Default API: `http://localhost:8080`

## Endpoints
- `GET /health`
- `POST /api/auth/register`
- `POST /api/auth/login`
- `GET /api/me` (Bearer token)
- `GET /ws?token=...`
