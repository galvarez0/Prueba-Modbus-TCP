package main

import (
  "context"
  "encoding/json"
  "errors"
  "fmt"
  "log"
  "net/http"
  "os"
  "strings"
  "time"

  "github.com/jackc/pgx/v5/pgxpool"
)

type Node struct {
  SensorID   string    `json:"sensor_id"`
  Type       string    `json:"type"`
  Name       *string   `json:"name,omitempty"`
  Description *string  `json:"description,omitempty"`
  Tags       []string  `json:"tags,omitempty"`
  Script     string    `json:"script"`
  Enabled    bool      `json:"enabled"`
  UpdatedAt  time.Time `json:"updated_at"`
}

type UpsertNodeRequest struct {
  Type        *string  `json:"type"`
  Name        *string  `json:"name"`
  Description *string  `json:"description"`
  Tags        []string `json:"tags"`
  Script      *string  `json:"script"`
  Enabled     *bool    `json:"enabled"`
}

func main() {
  addr := getenv("ADMIN_API_ADDR", ":8095")
  dsn := strings.TrimSpace(os.Getenv("TELEMETRY_POSTGRES_DSN"))
  if dsn == "" {
    log.Fatal("TELEMETRY_POSTGRES_DSN is required")
  }

  ctx := context.Background()
  pool, err := pgxpool.New(ctx, dsn)
  if err != nil {
    log.Fatal(err)
  }
  defer pool.Close()

  mux := http.NewServeMux()
  api := &apiServer{db: pool}

  mux.HandleFunc("/healthz", api.healthz)
  mux.HandleFunc("/api/nodes", api.nodes)         // GET, POST
  mux.HandleFunc("/api/nodes/", api.nodeByID)      // GET, PUT, PATCH, DELETE

  handler := withJSON(withCORS(mux))
  log.Printf("[admin-api] listening on %s", addr)
  if err := http.ListenAndServe(addr, handler); err != nil {
    log.Fatal(err)
  }
}

type apiServer struct {
  db *pgxpool.Pool
}

func (s *apiServer) healthz(w http.ResponseWriter, r *http.Request) {
  w.WriteHeader(http.StatusOK)
  _ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *apiServer) nodes(w http.ResponseWriter, r *http.Request) {
  switch r.Method {
  case http.MethodGet:
    s.listNodes(w, r)
  case http.MethodPost:
    s.createNode(w, r)
  default:
    http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
  }
}

func (s *apiServer) nodeByID(w http.ResponseWriter, r *http.Request) {
  sensorID := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
  sensorID = strings.Trim(sensorID, "/")
  if sensorID == "" {
    http.Error(w, "missing sensor_id", http.StatusBadRequest)
    return
  }

  switch r.Method {
  case http.MethodGet:
    s.getNode(w, r, sensorID)
  case http.MethodPut:
    s.upsertNode(w, r, sensorID)
  case http.MethodPatch:
    s.patchNode(w, r, sensorID)
  case http.MethodDelete:
    s.deleteNode(w, r, sensorID)
  default:
    http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
  }
}

func (s *apiServer) listNodes(w http.ResponseWriter, r *http.Request) {
  ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
  defer cancel()

  q := strings.TrimSpace(r.URL.Query().Get("q"))
  enabledParam := strings.TrimSpace(r.URL.Query().Get("enabled"))

  var where []string
  var args []any
  argn := 1

  if q != "" {
    // Search sensor_id, name, type
    where = append(where, fmt.Sprintf("(sensor_id ILIKE $%d OR COALESCE(name,'') ILIKE $%d OR COALESCE(type,'') ILIKE $%d)", argn, argn, argn))
    args = append(args, "%"+q+"%")
    argn++
  }
  if enabledParam != "" {
    b, err := parseBoolLoose(enabledParam)
    if err == nil {
      where = append(where, fmt.Sprintf("enabled = $%d", argn))
      args = append(args, b)
      argn++
    }
  }

  sql := `SELECT sensor_id, COALESCE(type,'custom'), name, description, COALESCE(tags, ARRAY[]::text[]), script, enabled, updated_at
          FROM starlark_scripts`
  if len(where) > 0 {
    sql += " WHERE " + strings.Join(where, " AND ")
  }
  sql += " ORDER BY sensor_id ASC"

  rows, err := s.db.Query(ctx, sql, args...)
  if err != nil {
    http.Error(w, err.Error(), http.StatusInternalServerError)
    return
  }
  defer rows.Close()

  nodes := make([]Node, 0, 64)
  for rows.Next() {
    var n Node
    var name, desc *string
    var tags []string
    if err := rows.Scan(&n.SensorID, &n.Type, &name, &desc, &tags, &n.Script, &n.Enabled, &n.UpdatedAt); err != nil {
      http.Error(w, err.Error(), http.StatusInternalServerError)
      return
    }
    n.Name = name
    n.Description = desc
    n.Tags = tags
    nodes = append(nodes, n)
  }
  _ = json.NewEncoder(w).Encode(map[string]any{"items": nodes})
}

func (s *apiServer) getNode(w http.ResponseWriter, r *http.Request, sensorID string) {
  ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
  defer cancel()

  var n Node
  var name, desc *string
  var tags []string
  err := s.db.QueryRow(ctx,
    `SELECT sensor_id, COALESCE(type,'custom'), name, description, COALESCE(tags, ARRAY[]::text[]), script, enabled, updated_at
     FROM starlark_scripts WHERE sensor_id=$1`, sensorID,
  ).Scan(&n.SensorID, &n.Type, &name, &desc, &tags, &n.Script, &n.Enabled, &n.UpdatedAt)
  if err != nil {
    http.Error(w, "not found", http.StatusNotFound)
    return
  }
  n.Name = name
  n.Description = desc
  n.Tags = tags
  _ = json.NewEncoder(w).Encode(n)
}

func (s *apiServer) createNode(w http.ResponseWriter, r *http.Request) {
  // POST /api/nodes expects full object including sensor_id.
  var body Node
  if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&body); err != nil {
    http.Error(w, "invalid json", http.StatusBadRequest)
    return
  }
  body.SensorID = strings.TrimSpace(body.SensorID)
  body.Type = strings.TrimSpace(body.Type)
  body.Script = strings.TrimSpace(body.Script)
  if body.SensorID == "" || body.Script == "" {
    http.Error(w, "sensor_id and script are required", http.StatusBadRequest)
    return
  }
  if body.Type == "" {
    body.Type = "custom"
  }

  ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
  defer cancel()

  _, err := s.db.Exec(ctx,
    `INSERT INTO starlark_scripts(sensor_id, enabled, script, type, name, description, tags)
     VALUES ($1,$2,$3,$4,$5,$6,$7)`
    , body.SensorID, body.Enabled, body.Script, body.Type, body.Name, body.Description, body.Tags)
  if err != nil {
    http.Error(w, err.Error(), http.StatusConflict)
    return
  }
  w.WriteHeader(http.StatusCreated)
  _ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *apiServer) upsertNode(w http.ResponseWriter, r *http.Request, sensorID string) {
  var req UpsertNodeRequest
  if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&req); err != nil {
    http.Error(w, "invalid json", http.StatusBadRequest)
    return
  }
  if req.Script != nil {
    sc := strings.TrimSpace(*req.Script)
    req.Script = &sc
  }

  // PUT means full replace of fields we support; if omitted -> set NULL/default.
  t := "custom"
  if req.Type != nil && strings.TrimSpace(*req.Type) != "" {
    t = strings.TrimSpace(*req.Type)
  }
  enabled := true
  if req.Enabled != nil {
    enabled = *req.Enabled
  }
  script := ""
  if req.Script != nil {
    script = *req.Script
  }
  if strings.TrimSpace(script) == "" {
    http.Error(w, "script is required", http.StatusBadRequest)
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
  defer cancel()

  _, err := s.db.Exec(ctx,
    `INSERT INTO starlark_scripts(sensor_id, enabled, script, type, name, description, tags)
     VALUES ($1,$2,$3,$4,$5,$6,$7)
     ON CONFLICT (sensor_id)
     DO UPDATE SET enabled=EXCLUDED.enabled, script=EXCLUDED.script, type=EXCLUDED.type, name=EXCLUDED.name, description=EXCLUDED.description, tags=EXCLUDED.tags`,
    sensorID, enabled, script, t, req.Name, req.Description, req.Tags,
  )
  if err != nil {
    http.Error(w, err.Error(), http.StatusInternalServerError)
    return
  }
  _ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *apiServer) patchNode(w http.ResponseWriter, r *http.Request, sensorID string) {
  var req UpsertNodeRequest
  if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&req); err != nil {
    http.Error(w, "invalid json", http.StatusBadRequest)
    return
  }

  sets := make([]string, 0, 6)
  args := make([]any, 0, 8)
  n := 1
  if req.Enabled != nil {
    sets = append(sets, fmt.Sprintf("enabled=$%d", n))
    args = append(args, *req.Enabled)
    n++
  }
  if req.Script != nil {
    sc := strings.TrimSpace(*req.Script)
    if sc == "" {
      http.Error(w, "script cannot be empty", http.StatusBadRequest)
      return
    }
    sets = append(sets, fmt.Sprintf("script=$%d", n))
    args = append(args, sc)
    n++
  }
  if req.Type != nil {
    tp := strings.TrimSpace(*req.Type)
    if tp == "" {
      tp = "custom"
    }
    sets = append(sets, fmt.Sprintf("type=$%d", n))
    args = append(args, tp)
    n++
  }
  if req.Name != nil {
    sets = append(sets, fmt.Sprintf("name=$%d", n))
    args = append(args, *req.Name)
    n++
  }
  if req.Description != nil {
    sets = append(sets, fmt.Sprintf("description=$%d", n))
    args = append(args, *req.Description)
    n++
  }
  if req.Tags != nil {
    sets = append(sets, fmt.Sprintf("tags=$%d", n))
    args = append(args, req.Tags)
    n++
  }

  if len(sets) == 0 {
    http.Error(w, "no fields to patch", http.StatusBadRequest)
    return
  }
  args = append(args, sensorID)

  ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
  defer cancel()
  sql := fmt.Sprintf("UPDATE starlark_scripts SET %s WHERE sensor_id=$%d", strings.Join(sets, ", "), n)
  ct, err := s.db.Exec(ctx, sql, args...)
  if err != nil {
    http.Error(w, err.Error(), http.StatusInternalServerError)
    return
  }
  if ct.RowsAffected() == 0 {
    http.Error(w, "not found", http.StatusNotFound)
    return
  }
  _ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *apiServer) deleteNode(w http.ResponseWriter, r *http.Request, sensorID string) {
  if sensorID == "default" {
    http.Error(w, "cannot delete default", http.StatusBadRequest)
    return
  }
  ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
  defer cancel()
  ct, err := s.db.Exec(ctx, `DELETE FROM starlark_scripts WHERE sensor_id=$1`, sensorID)
  if err != nil {
    http.Error(w, err.Error(), http.StatusInternalServerError)
    return
  }
  if ct.RowsAffected() == 0 {
    http.Error(w, "not found", http.StatusNotFound)
    return
  }
  _ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func withJSON(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json; charset=utf-8")
    next.ServeHTTP(w, r)
  })
}

func withCORS(next http.Handler) http.Handler {
  allow := getenv("CORS_ALLOW_ORIGIN", "*")
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Access-Control-Allow-Origin", allow)
    w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
    w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
    if r.Method == http.MethodOptions {
      w.WriteHeader(http.StatusNoContent)
      return
    }
    next.ServeHTTP(w, r)
  })
}

func getenv(k, def string) string {
  v := strings.TrimSpace(os.Getenv(k))
  if v == "" {
    return def
  }
  return v
}

func parseBoolLoose(s string) (bool, error) {
  switch strings.ToLower(strings.TrimSpace(s)) {
  case "1", "true", "t", "yes", "y", "on":
    return true, nil
  case "0", "false", "f", "no", "n", "off":
    return false, nil
  default:
    return false, errors.New("invalid bool")
  }
}
