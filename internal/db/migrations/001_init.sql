-- +goose Up
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE repos (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    path         TEXT NOT NULL UNIQUE,
    name         TEXT NOT NULL,
    indexed_files JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE tasks (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    repo_id      UUID NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    type         TEXT NOT NULL CHECK (type IN ('full', 'incremental')),
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'completed', 'failed', 'partial')),
    started_at   TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error        TEXT,
    stats        JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_tasks_repo_id_status ON tasks(repo_id, status);
CREATE INDEX idx_tasks_created_at ON tasks(created_at DESC);

CREATE TABLE functions (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    repo_id    UUID NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    file_path  TEXT NOT NULL,
    start_line INT NOT NULL,
    end_line   INT NOT NULL,
    source     TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(repo_id, file_path, name)
);

CREATE INDEX idx_functions_repo_id ON functions(repo_id);
CREATE INDEX idx_functions_file_path ON functions(repo_id, file_path);

CREATE TABLE call_edges (
    id        UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    repo_id   UUID NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    caller_id UUID NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
    callee_id UUID NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
    UNIQUE(caller_id, callee_id)
);

CREATE INDEX idx_call_edges_caller ON call_edges(caller_id);
CREATE INDEX idx_call_edges_callee ON call_edges(callee_id);

CREATE TABLE unresolved_edges (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    repo_id         UUID NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    caller_id       UUID NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
    callee_name_raw TEXT NOT NULL
);

CREATE INDEX idx_unresolved_edges_caller ON unresolved_edges(caller_id);

-- +goose Down
DROP TABLE IF EXISTS unresolved_edges;
DROP TABLE IF EXISTS call_edges;
DROP TABLE IF EXISTS functions;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS repos;
DROP EXTENSION IF EXISTS "uuid-ossp";
