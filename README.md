# NexTalk Backend

Private Discord-style chat/voice backend for a small trusted group.

## Current v1 features

- Register/login with bcrypt + JWT
- Optional private invite code
- PostgreSQL users, server membership, channels and persistent messages
- Automatic private NexTalk workspace with general/random + two voice rooms
- Realtime multi-user chat over WebSocket
- Online presence
- WebSocket reconnect support on the frontend
- WebRTC signaling for group voice
- Mute/deafen/leave controls in the frontend
- STUN/TURN support
- Docker deployment for API, PostgreSQL, frontend and coturn

## Local development

1. Create PostgreSQL database and run:

    psql "$DATABASE_URL" -f migrations/001_init.sql

2. Copy environment:

    cp .env.example .env

3. Use a strong JWT secret (32+ characters), then export the variables or load them with your process manager.

4. Start API:

    go run ./cmd/server

API defaults to http://localhost:8080.

## Production with Docker Compose

Copy:

    cp .env.deploy.example .env

Edit every CHANGE_ME value and set TURN_PUBLIC_IP to the public IPv4 of the server/router.

Start:

    docker compose up -d --build

Services:

- Frontend: http://127.0.0.1:3000
- API/WebSocket: http://127.0.0.1:8080
- PostgreSQL: internal only
- TURN: host UDP/TCP 3478 and UDP 49160-49200

## Cloudflare setup for miosmooth.com

Recommended public hostnames:

- nextalk.miosmooth.com -> http://localhost:3000 through Cloudflare Tunnel
- api.nextalk.miosmooth.com -> http://localhost:8080 through Cloudflare Tunnel
- turn.nextalk.miosmooth.com -> DNS-only A record to the public IP

Cloudflare Tunnel works for the web app, API and WebSocket. It does not replace a normal UDP TURN path.

For TURN, forward these router ports to the NexTalk server:

- TCP/UDP 3478
- UDP 49160-49200

If the internet connection is behind CGNAT and cannot receive forwarded ports, run coturn on a small VPS with a public IP instead.

## Security notes

- Never use the development JWT secret in production.
- Set INVITE_CODE so unknown visitors cannot create accounts.
- CORS_ORIGIN should contain only the actual frontend origins.
- Serve the public app only through HTTPS/WSS.
- TURN credentials are included in the frontend bundle in this v1 deployment; use strong credentials and rotate them if the app becomes public.
- Back up the PostgreSQL volume regularly.

## Backup

Example:

    docker compose exec -T postgres pg_dump -U nextalk_app NexTalk > nextalk-backup.sql

Restore into a fresh database with psql.
