CREATE TABLE IF NOT EXISTS jobs (
    id text PRIMARY KEY,
    filename text NOT NULL,
    status text NOT NULL CHECK (status IN ('queued', 'running', 'completed', 'failed', 'cancelled')),
    stage text NOT NULL CHECK (stage IN ('probe', 'transcribe', 'select', 'render', 'completed')),
    mode text NOT NULL,
    error text NOT NULL DEFAULT '',
    source_path text NOT NULL,
    duration double precision NOT NULL DEFAULT 0,
    transcript jsonb NOT NULL DEFAULT '[]',
    clips jsonb NOT NULL DEFAULT '[]',
    attempts integer NOT NULL DEFAULT 0,
    lease_token text NOT NULL DEFAULT '',
    lease_until timestamptz,
    available_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS jobs_queue_idx ON jobs (available_at, created_at) WHERE status = 'queued';
CREATE INDEX IF NOT EXISTS jobs_lease_idx ON jobs (lease_until) WHERE status = 'running';
