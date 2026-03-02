# Admin Console (Nodes)

This adds an **admin API** + **React UI** to manage executable Starlark nodes stored in Postgres table `starlark_scripts` (DB `telemetry`).

## What it manages

Each row in `telemetry.starlark_scripts` is treated as an executable node:

- `sensor_id`  -> node id
- `script`     -> Starlark logic
- `enabled`    -> active/inactive
- `updated_at` -> hot-reload signal for `starlark-actions`

Additional editor-friendly columns are added (non-breaking):

- `type` (TEXT)
- `name` (TEXT)
- `description` (TEXT)
- `tags` (TEXT[])

## Run with Docker Compose

```bash
docker compose --profile admin --profile actions up -d --build
```

Then:

- Admin API: http://127.0.0.1:8095
- Admin UI:  http://127.0.0.1:8096

## Admin API endpoints

- `GET /api/nodes?q=&enabled=true|false`
- `POST /api/nodes` (create)
- `GET /api/nodes/{sensor_id}`
- `PATCH /api/nodes/{sensor_id}`
- `DELETE /api/nodes/{sensor_id}` (cannot delete `default`)

## Caddy (Basic Auth) idea

If you proxy these behind Caddy with Basic Auth:

```caddyfile
@admin path /admin/*
handle @admin {
  basicauth {
    admin <bcrypt-hash>
  }
  handle_path /admin/* {
    reverse_proxy admin-ui:80
  }
}

@admin_api path /admin-api/*
handle @admin_api {
  basicauth {
    admin <bcrypt-hash>
  }
  handle_path /admin-api/* {
    reverse_proxy admin-api:8095
  }
}
```

In that setup, build the UI with `VITE_API_BASE=/admin-api` (same-origin).
